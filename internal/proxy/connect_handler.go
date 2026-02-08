package proxy

import (
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// handleConnect 處理 CONNECT 請求（HTTPS 代理）
func (h *ProxyHandler) handleConnect(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logrus.Errorf("Recovered panic in handleConnect for %s: %v", r.URL.String(), rec)
			http.Error(w, "Internal server error: unexpected panic", http.StatusInternalServerError)
		}
	}()

	// 設置 TLS 狀態為已連接
	w.WriteHeader(http.StatusOK)

	// 記錄連接開始
	logrus.Debugf("Starting tunnel for %s", r.URL.Host)

	// 解析目標主機和端口
	host, port, err := net.SplitHostPort(r.URL.Host)
	if err != nil {
		host = r.URL.Host
		port = "443"
	}

	_ = host
	_ = port

	// 使用隨機 Transport 連接到目標，每次都會從數據庫選擇新的代理
	transport, err := h.getRandomTransport(3) // 最多重試 3 次
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		logrus.Errorf("Failed to create transport: %v", err)
		return
	}

	// 創建連接
	conn, err := transport.Dial("tcp", r.URL.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		logrus.Errorf("Failed to connect to %s: %v", r.URL.Host, err)
		return
	}

	// 構建從客戶端到 proxy 的連接（hijack）
	hijacker, clientOk := w.(http.Hijacker)
	if !clientOk {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		conn.Close()
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		logrus.Errorf("Failed to hijack client connection: %v", err)
		conn.Close()
		return
	}

	// 設置連接超時
	deadline := w.Header().Get("X-Done")
	if deadline != "" {
		if d, parseErr := time.ParseDuration(deadline); parseErr == nil {
			clientConn.SetDeadline(time.Now().Add(d))
			conn.SetDeadline(time.Now().Add(d))
		}
	}

	// 使用協程進行雙向通信
	var wg sync.WaitGroup

	// 發送客戶端到目標的流量
	wg.Add(1)
	go func() {
		defer wg.Done()
		hijackClientToTarget(clientConn, conn)
	}()

	// 發送目標到客戶端的流量
	wg.Add(1)
	go func() {
		defer wg.Done()
		hijackTargetToClient(conn, clientConn)
	}()

	// 等待任務完成
	wg.Wait()

	// 關閉連接
	clientConn.Close()
	conn.Close()

	logrus.Debugf("Tunnel closed for %s", r.URL.Host)
}

// hijackClientToTarget 發送客戶端流量到目標
func hijackClientToTarget(clientConn, targetConn net.Conn) {
	defer func() {
		if rec := recover(); rec != nil {
			logrus.Errorf("Panic in hijackClientToTarget: %v", rec)
		}
		targetConn.Close()
	}()

	io.Copy(targetConn, clientConn)
}

// hijackTargetToClient 發送目標流量到客戶端
func hijackTargetToClient(targetConn, clientConn net.Conn) {
	defer func() {
		if rec := recover(); rec != nil {
			logrus.Errorf("Panic in hijackTargetToClient: %v", rec)
		}
		clientConn.Close()
	}()

	io.Copy(clientConn, targetConn)
}
