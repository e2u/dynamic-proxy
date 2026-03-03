package proxy

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/sirupsen/logrus"
)

// 使用 sync.Pool 為每個 goroutine 提供獨立的 rand.Rand 實例
var randPool = sync.Pool{
	New: func() any {
		return rand.New(rand.NewSource(time.Now().UnixNano()))
	},
}

func getRand() *rand.Rand {
	return randPool.Get().(*rand.Rand)
}

func putRand(r *rand.Rand) {
	randPool.Put(r)
}

// selectProxyFromDB 從數據庫中隨機選擇一個代理（使用蓄水池抽樣，不加载所有代理到内存）
func (h *ProxyHandler) selectProxyFromDB() (*Proxy, error) {
	logrus.Debugf("selectProxyFromDB: start")
	if h.BDB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var selectedProxy *Proxy
	count := 0

	r := getRand()
	defer putRand(r)

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
					count++
					// 蓄水池抽樣：以 1/count 的概率選擇當前代理
					if r.Intn(count) == 0 {
						selectedProxy = p
					}
				}
				return nil
			})
			if err != nil {
				logrus.Errorf("selectProxyFromDB: value iteration error: %v", err)
				return err
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	logrus.Debugf("selectProxyFromDB: found %d proxies", count)

	if count == 0 {
		return nil, fmt.Errorf("no available proxies in database")
	}

	return selectedProxy, nil
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