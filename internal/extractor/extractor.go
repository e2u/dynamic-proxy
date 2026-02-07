package extractor

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/PuerkitoBio/goquery"
	"github.com/e2u/dynamic-proxy/internal/proxy"
	"github.com/sirupsen/logrus"
)

func Extractor(proxiesChan chan<- *proxy.Proxy, body []byte) error {
	logrus.Debugf("extractor called, body length: %d", len(body))
	logrus.Tracef("extractor body: %s", string(body))
	extractors := []func(chan<- *proxy.Proxy, []byte) (bool, error){
		extractAndValidateProxies,
		extractProxiesFromHTMLTable,
	}
	for _, f := range extractors {
		ok, err := f(proxiesChan, body)
		if err != nil {
			logrus.Errorf("extractor error: %v", err)
		}
		if ok {
			break
		}
	}
	return nil
}

func extractProxiesFromHTMLTable(proxiesChan chan<- *proxy.Proxy, body []byte) (bool, error) {
	logrus.Infof("extractProxiesFromHTMLTable called")
	var wg sync.WaitGroup
	var totalProxyCount int64

	var removeTags = []string{"script", "style", "noscript", "iframe", "head", "meta", "link", "textarea", "nav"}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	for _, tag := range removeTags {
		doc.Find(tag).Remove()
	}
	doc.Find("*").Each(func(i int, s *goquery.Selection) {
		s.Get(0).Attr = nil
	})

	ipReg := regexp.MustCompile(`^(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})$`)
	portReg := regexp.MustCompile(`^(\d{2,5})$`)

	doc.Find("tr").Each(func(i int, s *goquery.Selection) {
		var ip, port string

		s.Find("td").Each(func(j int, td *goquery.Selection) {
			text := strings.TrimSpace(td.Text())
			if ip == "" && ipReg.MatchString(text) {
				ip = text
			} else if ip != "" && port == "" && portReg.MatchString(text) {
				port = text
			}
			if ip != "" && port != "" {
				return
			}
		})

		if ip != "" && port != "" {

			r := &proxy.Proxy{
				IP:   ip,
				Port: port,
			}
			wg.Add(1)
			go func(_p *proxy.Proxy) {
				defer wg.Done()
				if proxy.ValidProxy(_p) {
					proxiesChan <- _p
					atomic.AddInt64(&totalProxyCount, 1)
				}
			}(r)
		}
	})
	wg.Wait()
	logrus.Infof("extractProxiesFromHTMLTable done, totalProxyCount: %d", totalProxyCount)
	return totalProxyCount > 0, nil
}

func extractAndValidateProxies(proxiesChan chan<- *proxy.Proxy, body []byte) (bool, error) {
	logrus.Infof("extractAndValidateProxies called")
	var wg sync.WaitGroup
	seen := make(map[string]bool)
	var totalProxyCount int64

	reg1 := regexp.MustCompile(
		`(?i)(?:(?P<protocol>socks[45a]?|http|https)://)?` +
			`(?P<ip>(?:25[0-5]|2[0-4]\d|[01]?\d{1,2})\.(?:25[0-5]|2[0-4]\d|[01]?\d{1,2})\.(?:25[0-5]|2[0-4]\d|[01]?\d{1,2})\.(?:25[0-5]|2[0-4]\d|[01]?\d{1,2})):` +
			`(?P<port>\d{1,5})`)

	reg2 := regexp.MustCompile(
		`((?:25[0-5]|2[0-4]\d|[01]?\d{1,2})\.(?:25[0-5]|2[0-4]\d|[01]?\d{1,2})\.(?:25[0-5]|2[0-4]\d|[01]?\d{1,2})\.(?:25[0-5]|2[0-4]\d|[01]?\d{1,2}))` +
			`[:\s,;\|\(\[\{]+` +
			`(\d{1,5})(?:\D|$)`)

	reg3 := regexp.MustCompile(`(?i)\s*"\s*ip\s*"\s*:\s*"\s*((?:\d{1,3}\.){3}\d{1,3})\s*"[\s\S]*?"\s*port\s*"\s*:\s*"\s*(\d+)\s*"`)

	bodyStr := string(body)
	matches1 := reg1.FindAllStringSubmatch(bodyStr, -1)
	names1 := reg1.SubexpNames()

	// Helper function to validate port
	isValidPort := func(portStr string) bool {
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return false
		}
		return port > 0 && port <= 65535
	}

	for _, match := range matches1 {
		result := make(map[string]string)
		for i, name := range names1 {
			if i != 0 && name != "" {
				result[name] = match[i]
			}
		}

		// Validate port
		if !isValidPort(result["port"]) {
			continue
		}

		key := result["ip"] + ":" + result["port"]
		if seen[key] {
			continue
		}
		seen[key] = true

		r := &proxy.Proxy{
			IP:       result["ip"],
			Port:     result["port"],
			Protocol: strings.ToLower(result["protocol"]),
		}
		if r.Protocol == "" {
			r.Protocol = "http"
		}

		wg.Add(1)
		go func(p *proxy.Proxy) {
			defer wg.Done()
			if proxy.ValidProxy(p) {
				proxiesChan <- p
				atomic.AddInt64(&totalProxyCount, 1)
			}
		}(r)
	}

	for _, reg := range []*regexp.Regexp{reg2, reg3} {
		for _, m := range reg.FindAllStringSubmatch(bodyStr, -1) {
			if len(m) < 3 {
				continue
			}

			// Validate port
			if !isValidPort(m[2]) {
				continue
			}

			key := m[1] + ":" + m[2]
			if seen[key] {
				continue
			}
			seen[key] = true

			r := &proxy.Proxy{
				IP:       m[1],
				Port:     m[2],
				Protocol: "http",
			}

			wg.Add(1)
			go func(_p *proxy.Proxy) {
				defer wg.Done()
				if proxy.ValidProxy(_p) {
					proxiesChan <- _p
					atomic.AddInt64(&totalProxyCount, 1)
				}
			}(r)
		}
	}

	wg.Wait()
	logrus.Infof("extractAndValidateProxies done, totalProxyCount: %d", totalProxyCount)
	return totalProxyCount > 0, nil
}
