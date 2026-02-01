package main

import (
	"errors"

	"github.com/dgraph-io/badger/v4"
	"github.com/e2u/dynamic-proxy/internal/extractor"
	"github.com/e2u/dynamic-proxy/internal/fetcher"
	"github.com/e2u/dynamic-proxy/internal/proxy"
	"github.com/gocolly/colly/v2"
	"github.com/sirupsen/logrus"
)

var proxyUrls = []string{
	"https://free-proxy-list.net/en/",
	"https://free-proxy-list.net/en/socks-proxy.html",
	"https://free-proxy-list.net/en/uk-proxy.html",
	"https://free-proxy-list.net/en/ssl-proxy.html",
	"https://free-proxy-list.net/en/anonymous-proxy.html",
	"https://free-proxy-list.net/en/google-proxy.html",
	"https://www.us-proxy.org/",
}

func main() {
	logrus.SetLevel(logrus.InfoLevel)
	bdb, err := badger.Open(badger.DefaultOptions("proxy_badger_db"))
	if err != nil {
		logrus.Fatalf("failed to open badger db: %v", err)
		return
	}
	defer bdb.Close()
	proxiesChan := make(chan *proxy.Proxy, 100)
	defer close(proxiesChan)

	go func() {
		for p := range proxiesChan {
			bdb.Update(func(txn *badger.Txn) error {
				key := []byte(p.String())
				val := p.DumpJSON()
				_, err := txn.Get(key)
				if err != nil && errors.Is(err, badger.ErrKeyNotFound) {
					err = txn.Set(key, val)
					if err != nil {
						logrus.Errorf("failed to set proxy in db: %v", err)
						return err
					}
					logrus.Debugf("Added new proxy to db: %s", p.String())
					return nil
				}
				logrus.Debugf("Proxy already exists in db: %s", p.String())
				return txn.Set(key, val)
			})
		}
	}()

	c := fetcher.NewColly()
	logrus.Debugf("Colly collector initialized with User-Agent: %s", c.UserAgent)

	c.OnResponse(func(r *colly.Response) {
		logrus.Debugf("Visited: %s", r.Request.URL)
		logrus.Infof("%s Response Status Code: %d", r.Request.URL, r.StatusCode)
		logrus.Debugf("Response Body Length: %d", len(r.Body))

		err := extractor.Extractor(proxiesChan, r.Body)
		if err != nil {
			logrus.Errorf("extractor error: %v", err)
			return
		}
	})

	for _, url := range proxyUrls {
		logrus.Infof("Visiting URL: %s", url)
		err := c.Visit(url)
		if err != nil {
			logrus.Errorf("failed to visit %s: %v", url, err)
		}
	}
	c.Wait()
}
