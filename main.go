package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/e2u/dynamic-proxy/internal/extractor"
	"github.com/e2u/dynamic-proxy/internal/fetcher"
	"github.com/e2u/dynamic-proxy/internal/proxy"
	"github.com/gocolly/colly/v2"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
)

var proxyUrls = []string{
	// group 1
	"https://free-proxy-list.net/en/",
	"https://free-proxy-list.net/en/socks-proxy.html",
	"https://free-proxy-list.net/en/uk-proxy.html",
	"https://free-proxy-list.net/en/ssl-proxy.html",
	"https://free-proxy-list.net/en/anonymous-proxy.html",
	"https://free-proxy-list.net/en/google-proxy.html",
	// group 2
	"https://api.proxyscrape.com/v4/free-proxy-list/get?request=get_proxies&proxy_format=protocolipport&format=json",
	"https://proxylist.geonode.com/api/proxy-list?limit=500&page=1&sort_by=lastChecked&sort_type=desc",
	"https://cdn.jsdelivr.net/gh/proxifly/free-proxy-list@main/proxies/all/data.json",
}

var (
	bdb *badger.DB
)

func gatherProxies() {
	proxiesChan := make(chan *proxy.Proxy, 500)
	var wg sync.WaitGroup
	var newProxyCount, updateProxyCount int64

	wg.Add(1)
	go func() {
		defer wg.Done()
		for p := range proxiesChan {
			err := bdb.Update(func(txn *badger.Txn) error {
				key := []byte(p.String())
				val := p.DumpJSON()

				_, err := txn.Get(key)
				if err != nil {
					if errors.Is(err, badger.ErrKeyNotFound) {
						if err := txn.Set(key, val); err != nil {
							logrus.Errorf("failed to set proxy in db: %v", err)
							return err
						}
						logrus.Debugf("Added new proxy to db: %s", p.String())
						newProxyCount++
						return nil
					}
					return err
				}

				logrus.Debugf("Proxy already exists in db, updating: %s", p.String())
				updateProxyCount++
				return txn.Set(key, val)
			})

			if err != nil {
				logrus.Errorf("failed to update db for proxy %s: %v", p.String(), err)
			}
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

	c.OnError(func(r *colly.Response, err error) {
		logrus.Errorf("Request failed for %s: %v", r.Request.URL, err)
	})

	for _, url := range proxyUrls {
		logrus.Infof("Visiting URL: %s", url)
		err := c.Visit(url)
		if err != nil {
			logrus.Errorf("failed to visit %s: %v", url, err)
		}
	}

	c.Wait()
	close(proxiesChan)
	wg.Wait()
	logrus.Infof("All proxies have been processed, new: %d, updated: %d", newProxyCount, updateProxyCount)
}

func cleanupProxiesFromDB() (int, error) {
	if bdb == nil {
		return 0, errors.New("database not initialized")
	}

	var deletedCount int
	now := time.Now()
	maxAge := 72 * time.Hour

	err := bdb.Update(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 100
		it := txn.NewIterator(opts)
		defer it.Close()

		var keysToDelete [][]byte

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := item.KeyCopy(nil)

			err := item.Value(func(val []byte) error {
				p, err := proxy.LoadFromJSON(val)
				if err != nil {
					logrus.Warnf("failed to parse proxy, will delete: %v", err)
					keysToDelete = append(keysToDelete, key)
					return nil
				}
				shouldDelete := false
				if p.Disable {
					logrus.Debugf("Marking disabled proxy for deletion: %s", p.String())
					shouldDelete = true
				}
				if p.Updated.IsZero() {
					logrus.Debugf("Marking proxy with zero timestamp for deletion: %s", p.String())
					shouldDelete = true
				}

				if !p.Updated.IsZero() && now.Sub(p.Updated) > maxAge {
					logrus.Debugf("Marking stale proxy for deletion: %s (age: %v)", p.String(), now.Sub(p.Updated))
					shouldDelete = true
				}

				if shouldDelete {
					keysToDelete = append(keysToDelete, key)
				}

				return nil
			})

			if err != nil {
				return err
			}
		}

		for _, key := range keysToDelete {
			if err := txn.Delete(key); err != nil {
				logrus.Errorf("failed to delete key: %v", err)
				return err
			}
			deletedCount++
		}

		return nil
	})

	if err != nil {
		return 0, err
	}

	logrus.Infof("Cleanup completed: deleted %d proxies from database", deletedCount)
	return deletedCount, nil
}

func listAllProxiesFromDB() ([]*proxy.Proxy, error) {
	if bdb == nil {
		return nil, errors.New("database not initialized")
	}

	var proxies []*proxy.Proxy
	err := bdb.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 100
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				p, err := proxy.LoadFromJSON(val)
				if err != nil {
					logrus.Warnf("failed to parse proxy from db: %v", err)
					return nil
				}
				proxies = append(proxies, p)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	logrus.Infof("Loaded %d proxies from database", len(proxies))
	return proxies, nil
}

func checkAllProxiesHealth() error {
	var wg sync.WaitGroup
	ps, err := listAllProxiesFromDB()
	if err != nil {
		return err
	}
	for _, p := range ps {
		wg.Add(1)
		go func(_p *proxy.Proxy) {
			defer wg.Done()
			if proxy.ValidProxy(_p) {
				logrus.Infof("Proxy is healthy: %s", _p.String())
				return
			}
			// Mark proxy as disabled in DB
			err := bdb.Update(func(txn *badger.Txn) error {
				key := []byte(_p.String())
				p.Disable = true
				val := p.DumpJSON()
				return txn.Set(key, val)
			})
			if err != nil {
				logrus.Errorf("failed to mark proxy as disabled: %v", err)
				return
			} else {
				logrus.Infof("Marked proxy as disabled: %s", _p.String())
			}
		}(p)

	}
	wg.Wait()
	return nil
}

func main() {
	// Command line flags
	var (
		runOnce      = flag.Bool("once", false, "Run proxy gathering once and exit")
		listProxies  = flag.Bool("list", false, "List all proxies in database")
		checkHealth  = flag.Bool("check", false, "Check health of all proxies")
		cleanup      = flag.Bool("cleanup", false, "Clean up old/disabled proxies")
		logLevel     = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
		help         = flag.Bool("help", false, "Show help")
	)

	flag.Parse()

	if *help {
		flag.Usage()
		os.Exit(0)
	}

	// Set log level
	switch *logLevel {
	case "debug":
		logrus.SetLevel(logrus.DebugLevel)
	case "info":
		logrus.SetLevel(logrus.InfoLevel)
	case "warn":
		logrus.SetLevel(logrus.WarnLevel)
	case "error":
		logrus.SetLevel(logrus.ErrorLevel)
	default:
		logrus.SetLevel(logrus.InfoLevel)
	}

	var err error
	bdb, err = badger.Open(badger.DefaultOptions("proxy_badger_db"))
	if err != nil {
		logrus.Fatalf("failed to open badger db: %v", err)
		return
	}
	defer bdb.Close()

	// Handle command line options
	if *listProxies {
		ps, err := listAllProxiesFromDB()
		if err != nil {
			logrus.Errorf("listAllProxiesFromDB error: %v", err)
			os.Exit(1)
		}

		jb, err := json.MarshalIndent(ps, "", "\t")
		if err != nil {
			logrus.Fatalf("failed to marshal json: %v", err)
			os.Exit(1)
		}
		fmt.Printf("All Proxies in DB:\n%s\n", string(jb))
		return
	}

	if *checkHealth {
		err := checkAllProxiesHealth()
		if err != nil {
			logrus.Errorf("checkAllProxiesHealth error: %v", err)
			os.Exit(1)
		}
		logrus.Info("Health check completed")
		return
	}

	if *cleanup {
		count, err := cleanupProxiesFromDB()
		if err != nil {
			logrus.Errorf("cleanupProxiesFromDB error: %v", err)
			os.Exit(1)
		}
		logrus.Infof("Cleanup completed: deleted %d proxies", count)
		return
	}

	if *runOnce {
		gatherProxies()
		logrus.Info("Single run completed")
		return
	}

	// Default behavior - start cron scheduler
	checkAllProxiesHealth()
	cleanupProxiesFromDB()
	gatherProxies()

	c := cron.New()
	c.AddFunc("0 */1 * * *", func() {
		checkAllProxiesHealth()
	})

	c.AddFunc("30 */1 * * *", func() {
		cleanupProxiesFromDB()
	})

	c.AddFunc("0 */2 * * *", func() {
		gatherProxies()
	})
	c.Start()

	ps, err := listAllProxiesFromDB()
	if err != nil {
		logrus.Errorf("listAllProxiesFromDB error: %v", err)
		return
	}

	jb, err := json.MarshalIndent(ps, "", "\t")
	if err != nil {
		logrus.Fatalf("failed to marshal json: %v", err)
		return
	}
	logrus.Infof("All Proxies in DB:\n%s", string(jb))
	select {}
}
