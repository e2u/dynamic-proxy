package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/sirupsen/logrus"
)

type ProxyHandler struct {
	timeout time.Duration
	proxies []*Proxy
	BDB     *badger.DB
}

type ProxyServer struct {
	Proxies    []*Proxy
	HttpServer *http.Server
	Timeout    time.Duration
	ListenAddr string
	BDB        *badger.DB
}

type Options struct {
	Timeout    time.Duration
	ListenAddr string
}

type Option func(options *Options)

func WithTimeout(timeout time.Duration) Option {
	return func(options *Options) {
		options.Timeout = timeout
	}
}

func WithAddr(addr string) Option {
	return func(options *Options) {
		options.ListenAddr = addr
	}
}

func NewProxyServer(proxies []*Proxy, bdb *badger.DB, opts ...Option) *ProxyServer {
	cfg := &Options{
		Timeout:    30 * time.Second,
		ListenAddr: ":8080",
	}

	for _, opt := range opts {
		opt(cfg)
	}

	handler := &ProxyHandler{
		timeout: cfg.Timeout,
		proxies: proxies,
		BDB:     bdb,
	}
	httpServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler,
	}
	return &ProxyServer{
		ListenAddr: cfg.ListenAddr,
		Timeout:    cfg.Timeout,
		Proxies:    proxies,
		HttpServer: httpServer,
		BDB:        bdb,
	}
}

func (p *ProxyServer) Start() error {
	logrus.Infof("Starting proxy server on %s", p.ListenAddr)
	errCh := make(chan error, 1)
	go func() {
		if err := p.HttpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("failed to start proxy server: %w", err)
		}
	}()
	if err := waitForServer(p.ListenAddr, 5*time.Second); err != nil {
		if shutdownErr := p.HttpServer.Shutdown(context.Background()); shutdownErr != nil {
			logrus.Errorf("Shutdown during startup failure: %v", shutdownErr)
		}
		return err
	}
	return nil
}

func waitForServer(listenAddr string, timeout time.Duration) error {
	checkAddr := strings.Replace(listenAddr, "0.0.0.0", "127.0.0.1", 1)

	start := time.Now()
	for time.Since(start) < timeout {
		conn, err := net.DialTimeout("tcp", checkAddr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("server failed to start listening on %s within %v", checkAddr, timeout)
}

func (p *ProxyServer) Stop() error {
	logrus.Info("Stopping proxy server")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	if err := p.HttpServer.Shutdown(ctx); err != nil {
		logrus.Errorf("Shutdown error: %v", err)
	}
	cancel()
	logrus.Info("Proxy server shut down")
	return nil
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logrus.Errorf("Recovered panic in ServeHTTP for %s: %v", r.URL.String(), rec)
			http.Error(w, "Internal server error: unexpected panic", http.StatusInternalServerError)
		}
	}()

	r.Header.Del("Proxy-Connection")
	r.Header.Del("Proxy-Authenticate")
	r.Header.Del("Proxy-Authorization")

	if r.Method == http.MethodConnect {
		h.handleConnect(w, r)
		return
	}

	h.handleRegularRequest(w, r)
}

func (h *ProxyHandler) handleRegularRequest(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logrus.Errorf("Recovered panic in handleRegularRequest for %s: %v", r.URL.String(), rec)
			http.Error(w, "Internal server error: unexpected panic", http.StatusInternalServerError)
		}
	}()

	// 每個請求都從數據庫中隨機選擇一個代理
	proxy, err := h.selectProxyFromDB()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		logrus.Errorf("Failed to select proxy from DB: %v", err)
		return
	}

	// 記錄選中的上遊代理
	logrus.Infof("Selected upstream proxy: %s", proxy.String())

	transport := h.createTransport(proxy)
	client := &http.Client{
		Transport: transport,
		Timeout:   h.timeout,
	}

	req, err := http.NewRequestWithContext(context.Background(), r.Method, r.URL.String(), nil)
	if err != nil {
		logrus.Errorf("Failed to create new request: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// 只複製必要的頭部
	req.Header = make(http.Header)
	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Header.Set("X-Forwarded-For", r.RemoteAddr)

	resp, err := client.Do(req)
	if err != nil {
		logrus.Errorf("Request to %s via %s failed: %v", r.URL.String(), proxy.String(), err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 轉發響應頭
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 轉發狀態碼
	w.WriteHeader(resp.StatusCode)

	// 轉發響應體
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		logrus.Errorf("Error copying response body: %v", err)
	}

	// 記錄代理使用情況
	h.updateProxyCount(proxy)
	h.updateProxyHealth(proxy, true)
}

// updateProxyHealth 更新代理健康狀態
func (p *ProxyServer) updateProxyHealth(proxy *Proxy, healthy bool) {
	// 这里可以实现更新代理健康状态的逻辑
	// 暂时留空，后续可以根据需要实现
}