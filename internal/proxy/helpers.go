package proxy

import (
	"fmt"
	"math/rand"

	"github.com/dgraph-io/badger/v4"
	"github.com/sirupsen/logrus"
)

// selectProxy 從內存代理列表中隨機選擇一個代理
func (h *ProxyHandler) selectProxy() *Proxy {
	proxies := h.proxies
	if len(proxies) == 0 {
		return nil
	}

	randIndex := rand.Intn(len(proxies))
	proxy := proxies[randIndex]
	return proxy
}

// selectProxyFromDB 從數據庫中隨機選擇一個代理（每次調用都查詢數據庫）
func (h *ProxyHandler) selectProxyFromDB() (*Proxy, error) {
	if h.BDB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var proxies []*Proxy
	err := h.BDB.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 100
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				p, err := LoadFromJSON(val)
				if err != nil {
					logrus.Warnf("failed to parse proxy from DB: %v", err)
					return nil // 跳過損壞的條目
				}
				// 只選擇未禁用且已更新的代理
				if !p.Disable && !p.Updated.IsZero() {
					proxies = append(proxies, p)
				}
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

	if len(proxies) == 0 {
		return nil, fmt.Errorf("no available proxies in database")
	}

	// 隨機選擇一個代理
	randIndex := rand.Intn(len(proxies))
	return proxies[randIndex], nil
}

// selectProxyByWeight 使用權重選擇 proxy
func (h *ProxyHandler) selectProxyByWeight() *Proxy {
	proxies := h.proxies
	if len(proxies) == 0 {
		return nil
	}

	// 根據使用次數計算權重，使用次數越少權重越高
	totalCount := int64(0)
	for _, p := range proxies {
		totalCount += p.Count
	}

	if totalCount == 0 {
		return h.selectProxy()
	}

	// 隨機選擇一個 proxy
	randValue := int64(rand.Int63()) % totalCount
	cumulative := int64(0)
	for _, p := range proxies {
		weight := p.Count
		if cumulative+weight > randValue {
			return p
		}
		cumulative += weight
	}

	return proxies[len(proxies)-1]
}

// updateProxyCount 更新代理的使用次數
func (h *ProxyHandler) updateProxyCount(proxy *Proxy) {
	proxy.Count++
	if h.BDB != nil {
		proxyAddr := proxy.Addr
		if proxyAddr == "" {
			proxyAddr = proxy.IP + ":" + proxy.Port
		}
		key := fmt.Sprintf("proxy_count_%s", proxyAddr)
		if err := h.BDB.Update(func(txn *badger.Txn) error {
			item, err := txn.Get([]byte(key))
			if err == nil {
				var count int64
				if err := item.Value(func(v []byte) error {
					count = int64(v[0])
					return nil
				}); err != nil {
					return err
				}
				count++
				err = txn.Set([]byte(key), []byte{byte(count)})
				return err
			} else {
				err = txn.Set([]byte(key), []byte{1})
				return err
			}
		}); err != nil {
			logrus.Errorf("Failed to update proxy count for %s: %v", proxyAddr, err)
		}
	}
}

// updateProxyHealth 更新代理健康狀態
func (h *ProxyHandler) updateProxyHealth(proxy *Proxy, successful bool) {
	if h.BDB != nil {
		proxyAddr := proxy.Addr
		if proxyAddr == "" {
			proxyAddr = proxy.IP + ":" + proxy.Port
		}
		key := fmt.Sprintf("proxy_health_%s", proxyAddr)
		err := h.BDB.Update(func(txn *badger.Txn) error {
			if successful {
				// 成功使用，增加健康度分數
				item, err := txn.Get([]byte(key))
				if err != nil {
					return err
				}
				var health int
				if err := item.Value(func(v []byte) error {
					health = int(v[0])
					return nil
				}); err != nil {
					return err
				}
				health = min(health+1, 100)
				return txn.Set([]byte(key), []byte{byte(health)})
			} else {
				// 失敗使用，減少健康度分數
				item, getErr := txn.Get([]byte(key))
				if getErr != nil {
					return getErr
				}
				var health int
				if err := item.Value(func(v []byte) error {
					health = int(v[0])
					return nil
				}); err != nil {
					return err
				}
				health = max(health-10, 0)
				return txn.Set([]byte(key), []byte{byte(health)})
			}
		})
		if err != nil {
			logrus.Errorf("Failed to update proxy health for %s: %v", proxyAddr, err)
		}
	}
}