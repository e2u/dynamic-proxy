package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/sirupsen/logrus"
)

// HealthChecker 代理健康檢查器
type HealthChecker struct {
	interval    time.Duration
	timeout     time.Duration
	maxRetries  int
	httpClient  *http.Client
	proxyServer *ProxyServer
}

// NewHealthChecker 創建健康檢查器
func NewHealthChecker(interval, timeout time.Duration, maxRetries int, proxyServer *ProxyServer) *HealthChecker {
	return &HealthChecker{
		interval:   interval,
		timeout:    timeout,
		maxRetries: maxRetries,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		proxyServer: proxyServer,
	}
}

// Start 開始健康檢查
func (hc *HealthChecker) Start() {
	go func() {
		ticker := time.NewTicker(hc.interval)
		defer ticker.Stop()

		logrus.Info("Starting proxy health checker")

		for range ticker.C {
			hc.checkAllProxies()
		}
	}()
}

// Stop 停止健康檢查
func (hc *HealthChecker) Stop() {
	logrus.Info("Stopping proxy health checker")
}

// checkAllProxies 檢查所有代理
func (hc *HealthChecker) checkAllProxies() {
	proxies := hc.proxyServer.Proxies
	logrus.Debugf("Checking health of %d proxies", len(proxies))

	for _, proxy := range proxies {
		hc.checkProxy(proxy)
	}
}

// checkProxy 單獨檢查一個代理
func (hc *HealthChecker) checkProxy(proxy *Proxy) {
	// 構建健康檢查 URL
	checkURL := hc.buildCheckURL(proxy)
	if checkURL == "" {
		return
	}

	logrus.Debugf("Checking proxy %s (%s) - %s", proxy.Type, proxy.Addr, checkURL)

	// 重試機制
	success := false
	for i := 0; i < hc.maxRetries; i++ {
		if err := hc.attemptCheck(proxy, checkURL); err == nil {
			success = true
			break
		}
		logrus.Debugf("Proxy %s check attempt %d/%d failed: %v", proxy.Addr, i+1, hc.maxRetries, err)
		time.Sleep(1 * time.Second)
	}

	hc.updateProxyHealthStatus(proxy, success)
}

// attemptCheck 嘗試檢查代理健康狀態
func (hc *HealthChecker) attemptCheck(proxy *Proxy, checkURL string) error {
	var err error

	switch proxy.Type {
	case "http", "https":
		err = hc.checkHTTPProxy(proxy, checkURL)
	case "socks5":
		err = hc.checkSOCKS5Proxy(proxy)
	default:
		// 直連，簡單的連接測試
		err = hc.checkDirectConnection(proxy)
	}

	return err
}

// checkHTTPProxy 檢查 HTTP/HTTPS 代理
func (hc *HealthChecker) checkHTTPProxy(proxy *Proxy, checkURL string) error {
	// 構建帶有代理的 HTTP 請求
	r, err := http.NewRequest("GET", checkURL, nil)
	if err != nil {
		return err
	}

	r.Header.Set("User-Agent", "ProxyHealthChecker/1.0")

	// 使用 proxy 的 URL
	proxyURL, err := url.Parse(fmt.Sprintf("%s://%s", proxy.Type, proxy.Addr))
	if err != nil {
		return err
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			DialContext: (&net.Dialer{
				Timeout:   hc.timeout,
				KeepAlive: hc.timeout,
			}).DialContext,
			TLSHandshakeTimeout:   hc.timeout,
			ResponseHeaderTimeout: hc.timeout,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: hc.timeout,
	}

	resp, err := client.Do(r)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
}

// checkSOCKS5Proxy 檢查 SOCKS5 代理
func (hc *HealthChecker) checkSOCKS5Proxy(proxy *Proxy) error {
	// 尝试连接到 SOCKS5 代理
	dialer := &net.Dialer{
		Timeout: hc.timeout,
	}

	// 这里可以使用第三方库來檢查 SOCKS5 代理
	// 由於沒有直接使用 SOCKS5 客戶端，這裡簡單地測試連接性
	// 實際應用中應該使用專門的 SOCKS5 檢查庫
	conn, err := dialer.Dial("tcp", proxy.Addr)
	if err != nil {
		return err
	}
	conn.Close()

	return nil
}

// checkDirectConnection 檢查直連
func (hc *HealthChecker) checkDirectConnection(proxy *Proxy) error {
	dialer := &net.Dialer{
		Timeout: hc.timeout,
	}

	// 尝试连接到代理地址，測試網絡連接性
	conn, err := dialer.Dial("tcp", proxy.Addr)
	if err != nil {
		return err
	}
	conn.Close()

	return nil
}

// buildCheckURL 構建健康檢查 URL
func (hc *HealthChecker) buildCheckURL(proxy *Proxy) string {
	// 使用一個簡單的健康檢查端點
	// 實際應用中可以使用專門的健康檢查服務
	switch proxy.Type {
	case "http", "https":
		// 對於 HTTP/HTTPS 代理，檢查能否通過代理訪問一個公共 URL
		// 這裡使用 google.com 的根路徑作為示例
		return "https://www.google.com/"
	case "socks5":
		// SOCKS5 代理沒有 HTTP 檢查 URL
		return ""
	default:
		return ""
	}
}

// updateProxyHealthStatus 更新代理健康狀態
func (hc *HealthChecker) updateProxyHealthStatus(proxy *Proxy, healthy bool) {
	if hc.proxyServer != nil {
		hc.proxyServer.updateProxyHealth(proxy, healthy)
	}

	status := "unhealthy"
	if healthy {
		status = "healthy"
	}
	logrus.Infof("Proxy %s (%s) status: %s", proxy.Type, proxy.Addr, status)
}
