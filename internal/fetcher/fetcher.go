package fetcher

import (
	"github.com/gocolly/colly/v2"
	"github.com/sirupsen/logrus"
)

const (
	UserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.6 Mobile/15E148 Safari/604.1"
)

func NewColly() *colly.Collector {
	c := colly.NewCollector()
	c.Init()

	c.UserAgent = UserAgent
	c.IgnoreRobotsTxt = true
	c.Async = true

	err := c.Limits([]*colly.LimitRule{
		{
			DomainGlob: "*",
			//RandomDelay: 2 * time.Second,
			Parallelism: 3,
		},
	})
	if err != nil {
		logrus.Errorf("set colly limits: %v", err)
	}

	return c
}
