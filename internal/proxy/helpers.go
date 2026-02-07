package proxy

import (
	"math/rand"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// selectProxy 選擇一個可用的 proxy
func (h *ProxyHandler) selectProxy() *Proxy {
	proxies := h.proxies
	if len(proxies) == 0 {
		return nil
	}
	
	// 簡單的隨機選擇算法
	// 可以改進為根據優先順序和健康狀態進行選擇
	randIndex := int(time.Now().UnixNano()) % len(proxies)
	proxy := proxies[randIndex]
	return proxy
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
	randValue := int64(time.Now().UnixNano()) % totalCount
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
		key := fmt.Sprintf("proxy_count_%s", proxy.Addr)
		if err := h.BDB.Update(func(txn *badger.Txn) error {
			item, err := txn.Get([]byte(key))
			if err == nil {
				var count int64
				if err := item.Value(func(v []byte) error {
					count = int64(v[0])
					return nil
				})
				count++
				err = txn.Set([]byte(key), []byte{byte(count)})
			} else {
				err = txn.Set([]byte(key), []byte{1})
			}
			return err
		}); err != nil {
			logrus.Errorf("Failed to update proxy count for %s: %v", proxy.Addr, err)
		}
	}
}

// updateProxyHealth 更新代理健康狀態
func (h *ProxyHandler) updateProxyHealth(proxy *Proxy, successful bool) {
	if h.BDB != nil {
		key := fmt.Sprintf("proxy_health_%s", proxy.Addr)
		if err := h.BDB.Update(func(txn *badger.Txn) error {
			if successful {
				// 成功使用，增加健康度分數
				item, err := txn.Get([]byte(key))
				var health int
				if err == nil {
					item.Value(func(v []byte) error {
						health = int(v[0])
						return nil
					})
				}
				health = min(health+1, 100)
				err = txn.Set([]byte(key), []byte{byte(health)})
			} else {
				// 失敗使用，減少健康度分數
				item, err := txn.Get([]byte(key))
				var health int
				if err == nil {
					item.Value(func(v []byte) error {
						health = int(v[0])
						return nil
					})
				}
				health = max(health-10, 0)
				err = txn.Set([]byte(key), []byte{byte(health)})
			}
			return err
		}); err != nil {
			logrus.Errorf("Failed to update proxy health for %s: %v", proxy.Addr, err)
		}
	}
}