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
		// 每個請求都使用新的連接，這樣可以實現請求級別的代理更換
		MaxIdleConns:        0,
		IdleConnTimeout:     0 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// dialHTTP 使用 HTTP 代理連接
func (h *ProxyHandler) dialHTTP(ctx context.Context, dialer *net.Dialer, proxy *Proxy, addr string) (net.Conn, error) {
	logrus.Infof("Selected upstream proxy: %s", proxy.String())

	proxyAddr := proxy.Addr
	// 如果 Addr 為空，從 IP 和 Port 構建
	if proxyAddr == "" {
		proxyAddr = proxy.IP + ":" + proxy.Port
	}
	if !strings.HasPrefix(proxyAddr, "http://") && !strings.HasPrefix(proxyAddr, "https://") {
		proxyAddr = "http://" + proxyAddr
	}

	proxyURL, err := url.Parse(proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse proxy URL %s: %w", proxyAddr, err)
	}

	logrus.Debugf("dialHTTP: proxyAddr=%s, proxyURL.Host=%s, target=%s", proxyAddr, proxyURL.Host, addr)

	// 記錄選中的上遊代理
	logrus.Infof("Selected upstream proxy: %s", proxy.String())

	if proxy.User != "" && proxy.Pass != "" {
		proxyURL.User = url.UserPassword(proxy.User, proxy.Pass)
	}

	// 先連接到代理伺服器
	conn, err := dialer.DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to proxy %s: %w", proxyURL.Host, err)
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
	logrus.Infof("Selected upstream proxy: %s", proxy.String())

	// 解析 SOCKS5 代理地址
	proxyHost := proxy.IP
	proxyPort := proxy.Port

	// 連接到 SOCKS5 代理伺服器
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(proxyHost, proxyPort))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SOCKS5 proxy: %w", err)
	}

	// SOCKS5 握手
	authMethod := byte(0x00) // 無驗證
	if proxy.User != "" && proxy.Pass != "" {
		authMethod = byte(0x02) // 使用用戶名密碼驗證
	}

	// 發送握手請求
	_, err = conn.Write([]byte{0x05, 0x01, authMethod})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send SOCKS5 greeting: %w", err)
	}

	// 讀取握手響應
	buf := make([]byte, 2)
	_, err = conn.Read(buf)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to read SOCKS5 greeting response: %w", err)
	}

	if buf[0] != 0x05 {
		conn.Close()
		return nil, fmt.Errorf("SOCKS5 server version error")
	}

	if buf[1] == 0xff {
		conn.Close()
		return nil, fmt.Errorf("SOCKS5 authentication required but not supported")
	}

	// 如果需要用戶名密碼驗證
	if authMethod == 0x02 {
		err = h.socks5AuthUsernamePassword(conn, proxy.User, proxy.Pass)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("SOCKS5 username/password auth failed: %w", err)
		}
	}

	// 發送 CONNECT 請求
	err = h.socks5SendConnect(conn, addr)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SOCKS5 CONNECT failed: %w", err)
	}

	// 讀取 CONNECT 響應
	err = h.socks5ReadConnectResponse(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SOCKS5 CONNECT response failed: %w", err)
	}

	logrus.Debugf("SOCKS5 proxy %s:%s connected to %s", proxyHost, proxyPort, addr)
	return conn, nil
}

// socks5AuthUsernamePassword 使用用戶名密碼驗證
func (h *ProxyHandler) socks5AuthUsernamePassword(conn net.Conn, user, pass string) error {
	// 報文格式: VER(1) ULEN(1) USER(LEN) PLEN(1) PASS(LEN)
	authReq := make([]byte, 1+1+len(user)+1+len(pass))
	authReq[0] = 0x01 // VER
	authReq[1] = byte(len(user))
	copy(authReq[2:], user)
	authReq[2+len(user)] = byte(len(pass))
	copy(authReq[3+len(user):], pass)

	_, err := conn.Write(authReq)
	if err != nil {
		return err
	}

	// 讀取響應
	buf := make([]byte, 2)
	_, err = conn.Read(buf)
	if err != nil {
		return err
	}

	if buf[0] != 0x01 || buf[1] != 0x00 {
		return fmt.Errorf("SOCKS5 username/password auth failed")
	}

	return nil
}

// socks5SendConnect 發送 CONNECT 請求
func (h *ProxyHandler) socks5SendConnect(conn net.Conn, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}

	// 構建 CONNECT 請求
	req := make([]byte, 0)
	req = append(req, 0x05) // VER
	req = append(req, 0x01) // CMD: CONNECT
	req = append(req, 0x00) // RSV

	// 檢查是 IP 還是域名
	ip := net.ParseIP(host)
	if ip != nil {
		req = append(req, 0x01) // ATYP: IPv4
		if len(ip) == 16 {
			req[3] = 0x04 // ATYP: IPv6
			req = append(req, ip...)
		} else {
			req[3] = 0x01
			req = append(req, ip.To4()...)
		}
	} else {
		req = append(req, 0x03) // ATYP: DOMAINNAME
		req = append(req, byte(len(host)))
		req = append(req, host...)
	}

	// 添加端口
	portNum, err := net.LookupPort("tcp", port)
	if err != nil {
		return err
	}
	req = append(req, byte(portNum>>8), byte(portNum))

	_, err = conn.Write(req)
	return err
}

// socks5ReadConnectResponse 讀取 CONNECT 響應
func (h *ProxyHandler) socks5ReadConnectResponse(conn net.Conn) error {
	// 讀取響應頭 (5 bytes)
	header := make([]byte, 5)
	_, err := conn.Read(header)
	if err != nil {
		return err
	}

	if header[0] != 0x05 {
		return fmt.Errorf("invalid SOCKS5 version in response")
	}

	if header[1] != 0x00 {
		return fmt.Errorf("SOCKS5 CONNECT failed, status: %d", header[1])
	}

	// 讀取地址部分 (變長)
	// ATYP (1 byte) + ADDR (變長) + PORT (2 bytes)
	atyp := header[3]
	var addrLen int
	switch atyp {
	case 0x01: // IPv4
		addrLen = 4
	case 0x03: // DOMAINNAME
		// 需要先讀取域名長度
		lenByte := make([]byte, 1)
		_, err := conn.Read(lenByte)
		if err != nil {
			return err
		}
		addrLen = int(lenByte[0])
	case 0x04: // IPv6
		addrLen = 16
	default:
		return fmt.Errorf("unknown address type: %d", atyp)
	}

	// 讀取剩餘的地址和端口
	remaining := make([]byte, addrLen+2)
	_, err = conn.Read(remaining)
	if err != nil {
		return err
	}

	return nil
}

// getRandomTransport 隨機選擇代理並創建 Transport
func (h *ProxyHandler) getRandomTransport(_ int) (*http.Transport, error) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialer := &net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}

			// 每次請求都從數據庫中隨機選擇一個代理
			proxy, err := h.selectProxyFromDB()
			if err != nil {
				return nil, fmt.Errorf("failed to select proxy from DB: %w", err)
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
		// 每個請求都使用新的連接，這樣可以實現請求級別的代理更換
		MaxIdleConns:        0,
		IdleConnTimeout:     0 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return transport, nil
}
