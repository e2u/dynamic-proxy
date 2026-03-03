package fetcher

import (
	"math/rand"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/sirupsen/logrus"
)

// UserAgents 多個 UserAgent 用於輪換
var UserAgents = []string{
	// iOS Safari
	"Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.6 Mobile/15E148 Safari/604.1",
	// Android Chrome
	"Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Mobile Safari/537.36",
	// Windows Chrome
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Safari/537.36",
	// macOS Safari
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 13_5_1) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.6 Safari/605.1.15",
	// Windows Firefox
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:117.0) Gecko/20100101 Firefox/117.0",
	// macOS Chrome
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Safari/537.36",
}

// CollectorConfig 爬蟲配置
type CollectorConfig struct {
	UserAgent     string
	Timeout       time.Duration
	RandomDelay   time.Duration
	Parallelism   int
	MaxRetries    int
	IgnoreRobots  bool
}

// DefaultConfig 預設配置
var DefaultConfig = CollectorConfig{
	Timeout:      30 * time.Second,
	RandomDelay:  2 * time.Second,
	Parallelism:  2,
	MaxRetries:   3,
	IgnoreRobots: true,
}

// NewColly 創建 Collector（使用預設配置）
func NewColly() *colly.Collector {
	return NewCollyWithConfig(DefaultConfig)
}

// NewCollyWithConfig 創建 Collector（自定義配置）
func NewCollyWithConfig(cfg CollectorConfig) *colly.Collector {
	c := colly.NewCollector()
	c.Init()

	// 隨機選擇 UserAgent
	if cfg.UserAgent == "" {
		c.UserAgent = UserAgents[rand.Intn(len(UserAgents))]
	} else {
		c.UserAgent = cfg.UserAgent
	}

	c.IgnoreRobotsTxt = cfg.IgnoreRobots
	c.Async = true

	// 設置限制
	err := c.Limits([]*colly.LimitRule{
		{
			DomainGlob:  "*",
			Parallelism: cfg.Parallelism,
			RandomDelay: cfg.RandomDelay,
		},
	})
	if err != nil {
		logrus.Errorf("set colly limits: %v", err)
	}

	// 設置超時
	c.SetRequestTimeout(cfg.Timeout)

	// 重試機制
	retryCount := make(map[string]int)
	c.OnError(func(r *colly.Response, err error) {
		if r == nil {
			logrus.Debugf("request error: %v", err)
			return
		}

		key := r.Request.URL.String()
		count := retryCount[key]

		// 429 Too Many Requests - 等待後重試
		if r.StatusCode == 429 {
			if count < cfg.MaxRetries {
				retryCount[key] = count + 1
				waitTime := time.Duration(count+1) * 5 * time.Second
				logrus.Debugf("rate limited, waiting %v before retry %d/%d", waitTime, count+1, cfg.MaxRetries)
				time.Sleep(waitTime)
				r.Request.Do()
			} else {
				logrus.Warnf("max retries reached for %s", key)
			}
			return
		}

		// 5xx 錯誤 - 服務器錯誤，嘗試重試
		if r.StatusCode >= 500 && r.StatusCode < 600 {
			if count < cfg.MaxRetries {
				retryCount[key] = count + 1
				logrus.Debugf("server error %d, retrying %d/%d", r.StatusCode, count+1, cfg.MaxRetries)
				time.Sleep(time.Duration(count+1) * time.Second)
				r.Request.Do()
			}
			return
		}

		logrus.Debugf("request failed for %s: %d - %v", key, r.StatusCode, err)
	})

	return c
}

// GetRandomUserAgent 獲取隨機 UserAgent
func GetRandomUserAgent() string {
	return UserAgents[rand.Intn(len(UserAgents))]
}
