package proxy

import (
	"bufio"
	"fmt"
	"net"
	"strings"
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
	IP       string
	Port     string
	Protocol string
	Disable  bool
	Updated  time.Time
}

func (p *Proxy) Address() string {
	return fmt.Sprintf("%s://%s:%s", p.Protocol, p.IP, p.Port)
}

func (p *Proxy) String() string {
	return fmt.Sprintf("%s://%s:%s", p.Protocol, p.IP, p.Port)
}

func (p *Proxy) DumpJSON() []byte {
	return []byte(fmt.Sprintf(`{"ip":"%s","port":"%s","protocol":"%s","disable":%t,"updated":"%s"}`, p.IP, p.Port, p.Protocol, p.Disable, p.Updated.Format(time.RFC3339)))
}

func LoadProxyFromJSON(data []byte) (*Proxy, error) {
	var ip, port, protocol string
	var disable bool
	var updatedStr string

	_, err := fmt.Sscanf(string(data), `{"ip":"%s","port":"%s","protocol":"%s","disable":%t,"updated":"%s"}`, &ip, &port, &protocol, &disable, &updatedStr)
	if err != nil {
		return nil, err
	}

	updated, err := time.Parse(time.RFC3339, updatedStr)
	if err != nil {
		return nil, err
	}

	return &Proxy{
		IP:       ip,
		Port:     port,
		Protocol: protocol,
		Disable:  disable,
		Updated:  updated,
	}, nil
}

func ValidProxy(p *Proxy) bool {
	if p.IP == "" || p.IP == "0.0.0.0" || p.IP == "127.0.0.1" {
		return false
	}

	pp, err := guessProtocol(p.IP, p.Port)
	if err != nil {
		logrus.Debugf("guess protocol error for %s:%s: %v", p.IP, p.Port, err)
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
		if r.StatusCode > 204 {
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

	return valid
}

func guessProtocol(ip, port string) (string, error) {
	addr := net.JoinHostPort(ip, port)
	timeout := 8 * time.Second

	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return "", err
	}

	if err == nil {
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(timeout))
		fmt.Fprintf(conn, "GET http://www.google.com HTTP/1.1\r\nHost: www.google.com\r\n\r\n")
		reader := bufio.NewReader(conn)
		line, _ := reader.ReadString('\n')
		if strings.Contains(line, "HTTP/1") {
			return "http", nil
		}
	}

	conn, err = net.DialTimeout("tcp", addr, timeout)
	if err == nil {
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(timeout))
		fmt.Fprintf(conn, "CONNECT www.google.com:443 HTTP/1.1\r\nHost: www.google.com:443\r\n\r\n")
		reader := bufio.NewReader(conn)
		line, _ := reader.ReadString('\n')
		if strings.Contains(line, "HTTP/1") && strings.Contains(line, "200") {
			return "https", nil
		}
	}
	conn, err = net.DialTimeout("tcp", addr, timeout)
	if err == nil {
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(timeout))
		conn.Write([]byte{5, 1, 0})
		buf := make([]byte, 2)
		_, err = conn.Read(buf)
		if err == nil && buf[0] == 5 && buf[1] == 0 {
			conn.Write([]byte{5, 1, 0, 1, 8, 8, 8, 8, 0, 53})
			buf = make([]byte, 10)
			_, err = conn.Read(buf)
			if err == nil && buf[0] == 5 && buf[1] == 0 {
				return "socks5", nil
			}
		}
	}
	return "http", nil
}
