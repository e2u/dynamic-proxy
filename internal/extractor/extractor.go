package extractor

import (
	"bytes"
	"regexp"
	"strings"
	"sync"

	"github.com/PuerkitoBio/goquery"
	"github.com/e2u/dynamic-proxy/internal/proxy"
	"github.com/sirupsen/logrus"
)

func Extractor(proxiesChan chan<- *proxy.Proxy, body []byte) error {
	logrus.Debugf("extractor called, body length: %d", len(body))
	extractors := []func(chan<- *proxy.Proxy, []byte) (bool, error){
		extractor01,
		extractor02,
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

func extractor02(proxiesChan chan<- *proxy.Proxy, body []byte) (bool, error) {
	logrus.Infof("extractor02 called")
	var wg sync.WaitGroup
	var hasResult bool

	var removeTags = []string{"script", "style", "noscript", "iframe", "head", "meta", "link", "textarea", "nav"}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return hasResult, err
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
			if ipReg.MatchString(text) {
				ip = text
			}
			if ip != "" && portReg.MatchString(text) {
				port = text
			}
			if ip != "" && port != "" {
				r := &proxy.Proxy{
					IP:   ip,
					Port: port,
				}
				ip = ""
				port = ""

				wg.Add(1)
				go func(p *proxy.Proxy) {
					defer wg.Done()
					if proxy.ValidProxy(r) {
						hasResult = true
						proxiesChan <- r
					}
				}(r)
			}
		})
	})
	wg.Wait()
	return hasResult, nil
}

func extractor01(proxiesChan chan<- *proxy.Proxy, body []byte) (bool, error) {
	logrus.Infof("extractor01 called")
	var wg sync.WaitGroup
	var hasResult bool

	reg := regexp.MustCompile(
		`((?:25[0-5]|2[0-4]\d|1?\d{1,2})\.(?:25[0-5]|2[0-4]\d|1?\d{1,2})\.(?:25[0-5]|2[0-4]\d|1?\d{1,2})\.(?:25[0-5]|2[0-4]\d|1?\d{1,2}))` +
			`[:\s,;\|\(\[\{]+` +
			`(\d{1,5})` +
			`(?:\D|$)`)

	for _, m := range reg.FindAllStringSubmatch(string(body), -1) {
		if len(m) < 3 {
			continue
		}
		r := &proxy.Proxy{
			IP:   m[1],
			Port: m[2],
		}

		wg.Add(1)

		go func(p *proxy.Proxy) {
			defer wg.Done()
			if proxy.ValidProxy(p) {
				hasResult = true
				proxiesChan <- p
			}
		}(r)
	}
	wg.Wait()

	return hasResult, nil
}
