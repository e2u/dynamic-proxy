package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/e2u/dynamic-proxy/internal/fetcher"
	"github.com/gocolly/colly/v2"
	"github.com/sirupsen/logrus"
)

var testURLs = []string{
	"https://www.google.com/generate_204",
	"http://www.gstatic.com/generate_204",
	"https://connectivitycheck.gstatic.com/generate_204",
	"http://edge-http.microsoft.com/captiveportal/generate_204",
	"http://cp.cloudflare.com/generate_204",
}

func randomTestURL() string {
	return testURLs[time.Now().UnixNano()%int64(len(testURLs))]
}

type Proxy struct {
	IP       string    `json:"ip"`
	Port     string    `json:"port"`
	Protocol string    `json:"protocol"`
	Disable  bool      `json:"disable"`
	Updated  time.Time `json:"updated"`
}

func (p *Proxy) Address() string {
	return fmt.Sprintf("%s://%s:%s", p.Protocol, p.IP, p.Port)
}

func (p *Proxy) String() string {
	return fmt.Sprintf("%s://%s:%s", p.Protocol, p.IP, p.Port)
}

func (p *Proxy) DumpJSON() []byte {
	data, err := json.Marshal(p)
	if err != nil {
		logrus.Errorf("failed to marshal proxy: %v", err)
		return nil
	}
	return data
}

func LoadFromJSON(data []byte) (*Proxy, error) {
	var p Proxy
	err := json.Unmarshal(data, &p)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func ValidProxy(p *Proxy) bool {
	if p.IP == "" || p.IP == "0.0.0.0" || p.IP == "127.0.0.1" {
		return false
	}

	pp, err := determineConnectionProtocol(p.IP, p.Port)
	if err != nil {
		return false
	}

	p.Protocol = pp
	if p.Protocol == "" {
		return false
	}

	var valid bool

	c := fetcher.NewColly()
	c.Limit(&colly.LimitRule{
		RandomDelay: 1 * time.Second,
	})
	c.SetRequestTimeout(10 * time.Second)

	c.SetProxy(p.String())
	c.OnError(func(r *colly.Response, err error) {
		if r != nil && r.StatusCode > 204 {
			logrus.Debugf("proxy %s error: %v", p.String(), err)
		}
	})

	c.OnResponseHeaders(func(r *colly.Response) {
		if r.StatusCode == 204 {
			logrus.Debugf("valid proxy found: %s", p.String())
			valid = true
		}
	})

	c.Visit(randomTestURL())
	c.Wait()

	if valid {
		p.Updated = time.Now()
		p.Disable = false
		logrus.Infof("validated proxy: %s", p.String())
	} else {
		p.Disable = true
	}

	return valid
}

func determineConnectionProtocol(ip, port string) (string, error) {
	addr := net.JoinHostPort(ip, port)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		logrus.Tracef("TCP connection failed for %s: %v", addr, err)
		return "", fmt.Errorf("connection failed: %w", err)
	}
	conn.Close()

	overallTimeout := 20 * time.Second
	dialTimeout := 8 * time.Second

	type result struct {
		protocol string
		priority int
	}

	ctx, cancel := context.WithTimeout(context.Background(), overallTimeout)
	defer cancel()

	resultChan := make(chan result, 3)
	var wg sync.WaitGroup

	checkers := []struct {
		protocol string
		priority int
		check    func(context.Context, net.Conn) bool
	}{
		{"socks5", 1, checkSOCKS5},
		{"http", 2, checkHTTP},
		{"https", 3, checkHTTPS},
	}

	for _, checker := range checkers {
		wg.Add(1)
		go func(c struct {
			protocol string
			priority int
			check    func(context.Context, net.Conn) bool
		}) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			default:
			}

			dialCtx, dialCancel := context.WithTimeout(ctx, dialTimeout)
			defer dialCancel()

			var d net.Dialer
			conn, err := d.DialContext(dialCtx, "tcp", addr)
			if err != nil {
				logrus.Tracef("failed to connect to %s for %s check: %v", addr, c.protocol, err)
				return
			}
			defer conn.Close()

			if err := conn.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
				return
			}

			if c.check(ctx, conn) {
				select {
				case resultChan <- result{c.protocol, c.priority}:
					cancel()
				case <-ctx.Done():
				}
			}
		}(checker)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	var bestResult *result
	for r := range resultChan {
		if bestResult == nil || r.priority < bestResult.priority {
			bestResult = &r
		}
	}

	if bestResult != nil {
		return bestResult.protocol, nil
	}

	logrus.Tracef("protocol detection failed but TCP connected, defaulting to http for %s", addr)
	return "http", nil
}

func checkSOCKS5(ctx context.Context, conn net.Conn) bool {
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		logrus.Tracef("[checkSOCKS5] failed to write greeting: %v", err)
		return false
	}

	greetingBuf := make([]byte, 2)

	type readResult struct {
		success bool
		err     error
	}

	greetingDone := make(chan readResult, 1)
	go func() {
		_, err := io.ReadFull(conn, greetingBuf)
		greetingDone <- readResult{
			success: err == nil && greetingBuf[0] == 5 && greetingBuf[1] == 0,
			err:     err,
		}
	}()

	select {
	case result := <-greetingDone:
		if !result.success {
			if result.err != nil {
				logrus.Tracef("[checkSOCKS5] greeting read failed: %v", result.err)
			} else {
				logrus.Tracef("[checkSOCKS5] invalid greeting response: [%d, %d]", greetingBuf[0], greetingBuf[1])
			}
			return false
		}
	case <-ctx.Done():
		logrus.Tracef("[checkSOCKS5] context cancelled during greeting")
		return false
	case <-time.After(2 * time.Second):
		logrus.Tracef("[checkSOCKS5] timeout waiting for greeting response")
		return false
	}

	// SOCKS5 CONNECT: VER(1) CMD(1) RSV(1) ATYP(1) DST.ADDR(4) DST.PORT(2)
	connectRequest := []byte{
		5,          // VER: SOCKS5
		1,          // CMD: CONNECT
		0,          // RSV: Reserved
		1,          // ATYP: IPv4
		8, 8, 8, 8, // DST.ADDR: 8.8.8.8
		0, 53, // DST.PORT: 53
	}

	if _, err := conn.Write(connectRequest); err != nil {
		logrus.Tracef("[checkSOCKS5] failed to write CONNECT request: %v", err)
		return false
	}

	connectHeaderBuf := make([]byte, 4)

	connectDone := make(chan readResult, 1)
	go func() {
		_, err := io.ReadFull(conn, connectHeaderBuf)
		if err != nil {
			connectDone <- readResult{success: false, err: err}
			return
		}

		if connectHeaderBuf[0] != 5 {
			connectDone <- readResult{success: false, err: fmt.Errorf("invalid version: %d", connectHeaderBuf[0])}
			return
		}

		if connectHeaderBuf[1] != 0 {
			connectDone <- readResult{success: false, err: fmt.Errorf("connection failed, reply code: %d", connectHeaderBuf[1])}
			return
		}

		atyp := connectHeaderBuf[3]
		var remainingBytes int

		switch atyp {
		case 1: // IPv4
			remainingBytes = 4 + 2 // 4 bytes addr + 2 bytes port
		case 3: // Domain name
			lenBuf := make([]byte, 1)
			if _, err := io.ReadFull(conn, lenBuf); err != nil {
				connectDone <- readResult{success: false, err: err}
				return
			}
			remainingBytes = int(lenBuf[0]) + 2 // domain length + 2 bytes port
		case 4: // IPv6
			remainingBytes = 16 + 2 // 16 bytes addr + 2 bytes port
		default:
			connectDone <- readResult{success: false, err: fmt.Errorf("unknown address type: %d", atyp)}
			return
		}

		remainingBuf := make([]byte, remainingBytes)
		if _, err := io.ReadFull(conn, remainingBuf); err != nil {
			connectDone <- readResult{success: false, err: err}
			return
		}

		connectDone <- readResult{success: true, err: nil}
	}()

	select {
	case result := <-connectDone:
		if !result.success {
			if result.err != nil {
				logrus.Tracef("[checkSOCKS5] CONNECT failed: %v", result.err)
			}
			return false
		}
		logrus.Tracef("[checkSOCKS5] successfully validated SOCKS5 proxy")
		return true
	case <-ctx.Done():
		logrus.Tracef("[checkSOCKS5] context cancelled during CONNECT")
		return false
	case <-time.After(2 * time.Second):
		logrus.Tracef("[checkSOCKS5] timeout waiting for CONNECT response")
		return false
	}
}

func checkHTTPS(ctx context.Context, conn net.Conn) bool {
	request := "CONNECT www.google.com:443 HTTP/1.1\r\n" +
		"Host: www.google.com:443\r\n" +
		"User-Agent: Mozilla/5.0\r\n" +
		"Proxy-Connection: keep-alive\r\n" +
		"\r\n"

	if _, err := conn.Write([]byte(request)); err != nil {
		logrus.Tracef("[checkHTTPS] failed to write CONNECT request: %v", err)
		return false
	}

	reader := bufio.NewReader(conn)

	type readResult struct {
		line string
		err  error
	}

	done := make(chan readResult, 1)
	go func() {
		line, err := reader.ReadString('\n')
		done <- readResult{line: line, err: err}
	}()

	var result readResult
	select {
	case result = <-done:
		if result.err != nil {
			logrus.Tracef("[checkHTTPS] failed to read response: %v", result.err)
			return false
		}
	case <-ctx.Done():
		logrus.Tracef("[checkHTTPS] context cancelled")
		return false
	case <-time.After(3 * time.Second):
		logrus.Tracef("[checkHTTPS] timeout waiting for response")
		return false
	}

	line := strings.TrimSpace(result.line)
	logrus.Tracef("[checkHTTPS] received: %s", line)

	if !strings.HasPrefix(line, "HTTP/1.1 ") && !strings.HasPrefix(line, "HTTP/1.0 ") {
		return false
	}

	parts := strings.Fields(line)
	if len(parts) < 2 {
		return false
	}

	statusCode := parts[1]

	if statusCode != "200" {
		logrus.Tracef("[checkHTTPS] non-200 status code: %s (full response: %s)", statusCode, line)
		return false
	}

	return true
}

func checkHTTP(ctx context.Context, conn net.Conn) bool {
	request := "GET http://www.gstatic.com/generate_204 HTTP/1.1\r\n" +
		"Host: www.gstatic.com\r\n" +
		"User-Agent: Mozilla/5.0\r\n" +
		"Proxy-Connection: close\r\n" +
		"\r\n"

	if _, err := conn.Write([]byte(request)); err != nil {
		logrus.Tracef("[checkHTTP] failed to write HTTP request: %v", err)
		return false
	}

	reader := bufio.NewReader(conn)

	type readResult struct {
		line string
		err  error
	}

	done := make(chan readResult, 1)
	go func() {
		line, err := reader.ReadString('\n')
		done <- readResult{line: line, err: err}
	}()

	var result readResult
	select {
	case result = <-done:
		if result.err != nil {
			logrus.Tracef("[checkHTTP] failed to read response: %v", result.err)
			return false
		}
	case <-ctx.Done():
		logrus.Tracef("[checkHTTP] context cancelled")
		return false
	case <-time.After(3 * time.Second):
		logrus.Tracef("[checkHTTP] timeout waiting for response")
		return false
	}

	line := strings.TrimSpace(result.line)
	logrus.Tracef("[checkHTTP] received: %s", line)

	if !strings.HasPrefix(line, "HTTP/1.1 ") && !strings.HasPrefix(line, "HTTP/1.0 ") {
		return false
	}

	parts := strings.Fields(line)
	if len(parts) < 2 {
		return false
	}

	statusCode := parts[1]

	if len(statusCode) != 3 {
		return false
	}

	for _, c := range statusCode {
		if c < '0' || c > '9' {
			return false
		}
	}

	firstDigit := statusCode[0]
	if firstDigit != '2' && firstDigit != '3' {
		logrus.Tracef("[checkHTTP] received non-success status code: %s", statusCode)
		return false
	}

	return true
}
