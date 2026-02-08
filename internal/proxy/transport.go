package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// createTransport 根據代理配置創建 HTTP Transport
func (h *ProxyHandler) createTransport(proxy *Proxy) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialer := &net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}

			switch proxy.Protocol {
			case "http":
				return h.dialHTTP(ctx, dialer, proxy, addr)
			case "socks5":
				return h.dialSOCKS5(ctx, dialer, proxy, addr)
			default:
				// Direct connection
				return dialer.DialContext(ctx, network, addr)
			}
		},
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
}

// dialHTTP 使用 HTTP 代理連接
func (h *ProxyHandler) dialHTTP(ctx context.Context, dialer *net.Dialer, proxy *Proxy, addr string) (net.Conn, error) {
	proxyAddr := proxy.Addr
	if !strings.HasPrefix(proxyAddr, "http://") && !strings.HasPrefix(proxyAddr, "https://") {
		proxyAddr = "http://" + proxyAddr
	}

	proxyURL, err := url.Parse(proxyAddr)
	if err != nil {
		return nil, err
	}

	if proxy.User != "" && proxy.Pass != "" {
		proxyURL.User = url.UserPassword(proxy.User, proxy.Pass)
	}

	// 先連接到代理伺服器
	conn, err := dialer.DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, err
	}

	// 發送 CONNECT 請求
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", addr, addr)
	_, err = conn.Write([]byte(connectReq))
	if err != nil {
		conn.Close()
		return nil, err
	}

	// 讀取代理響應
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		conn.Close()
		return nil, err
	}

	resp := string(buf[:n])
	logrus.Debugf("Proxy %s response: %s", proxyAddr, resp)

	if !strings.Contains(strings.ToLower(resp), "200 connection established") {
		conn.Close()
		return nil, fmt.Errorf("proxy %s failed to establish connection: %s", proxyAddr, resp)
	}

	return conn, nil
}

// dialSOCKS5 使用 SOCKS5 代理連接
func (h *ProxyHandler) dialSOCKS5(ctx context.Context, dialer *net.Dialer, proxy *Proxy, addr string) (net.Conn, error) {
	// 暂时不实现 SOCKS5 功能
	return nil, fmt.Errorf("SOCKS5 not implemented")
}

// getRandomTransport 隨機選擇代理並創建 Transport
func (h *ProxyHandler) getRandomTransport(maxRetries int) (*http.Transport, error) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialer := &net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}

			// 重試選擇代理
			for i := 0; i < maxRetries; i++ {
				proxy := h.selectProxyByWeight()
				if proxy == nil {
					continue
				}

				switch proxy.Protocol {
				case "http":
					conn, err := h.dialHTTP(ctx, dialer, proxy, addr)
					if err == nil {
						return conn, nil
					}
					logrus.Debugf("HTTP proxy %s failed (retry %d/%d): %v", proxy.Addr, i+1, maxRetries, err)
				case "socks5":
					conn, err := h.dialSOCKS5(ctx, dialer, proxy, addr)
					if err == nil {
						return conn, nil
					}
					logrus.Debugf("SOCKS5 proxy %s failed (retry %d/%d): %v", proxy.Addr, i+1, maxRetries, err)
				default:
					// Direct connection
					conn, err := dialer.DialContext(ctx, network, addr)
					if err != nil {
						return nil, err
					}
					return conn, nil
				}
			}

			return nil, fmt.Errorf("all proxy attempts failed after %d retries", maxRetries)
		},
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
	return transport, nil
}
