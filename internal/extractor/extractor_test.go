package extractor

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/e2u/dynamic-proxy/internal/proxy"
	"github.com/e2u/e2util/e2test"
	"github.com/sirupsen/logrus"
)

func Helper_loadTestData(filename string) []byte {
	b, err := os.ReadFile("./test_datas/" + filename)
	if err == nil {
		fmt.Println("Loaded test data from", filename)
		return b
	}
	fmt.Println("Failed to load test data from", filename, ":", err)
	return nil
}

func TestMain(m *testing.M) {
	e2test.Chroot()
	os.Exit(m.Run())
}

func TestExtractor(t *testing.T) {
	logrus.SetLevel(logrus.InfoLevel)

	tests := []struct {
		name     string
		filename string
		url      string
	}{
		{"free-proxy-list-main", "free-proxy-list.net_en.html", "https://free-proxy-list.net/en/"},
		{"free-proxy-list-socks", "en_socks-proxy.html", "https://free-proxy-list.net/en/socks-proxy.html"},
		{"free-proxy-list-ssl", "en_ssl-proxy.html", "https://free-proxy-list.net/en/ssl-proxy.html"},
		{"free-proxy-list-anonymous", "en_anonymous-proxy.html", "https://free-proxy-list.net/en/anonymous-proxy.html"},
		{"free-proxy-list-uk", "en_uk-proxy.html", "https://free-proxy-list.net/en/uk-proxy.html"},
		{"free-proxy-list-google", "en_google-proxy.html", "https://free-proxy-list.net/en/google-proxy.html"},
		{"us-proxy-org", "www.us-proxy.org.html", "https://www.us-proxy.org/"},
		{"proxyscrape", "api.proxyscrape.com.json", "https://api.proxyscrape.com/"},
		{"geonode-01", "proxylist.geonode.com_01.json", "https://proxylist.geonode.com/"},
		{"geonode-02", "proxylist.geonode.com_02.json", "https://proxylist.geonode.com/"},
		{"geonode-03", "proxylist.geonode.com_03.json", "https://proxylist.geonode.com/"},
		{"jsdelivr", "cdn.jsdelivr.net.json", "https://cdn.jsdelivr.net/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := Helper_loadTestData(tt.filename)
			if body == nil {
				t.Skipf("test data not found: %s", tt.filename)
			}

			proxiesChan := make(chan *proxy.Proxy, 500)
			var extractedCount int64

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for p := range proxiesChan {
					t.Logf("Extracted proxy: %s:%s (%s)", p.IP, p.Port, p.Protocol)
					extractedCount++
				}
			}()

			err := Extractor(proxiesChan, body, tt.url)
			close(proxiesChan)
			wg.Wait()

			if err != nil {
				t.Errorf("Extractor failed: %v", err)
			}

			t.Logf("Extracted %d proxies from %s", extractedCount, tt.name)
		})
	}
}

func TestExtractorAutoDetect(t *testing.T) {
	logrus.SetLevel(logrus.DebugLevel)

	// 測試無 URL 情況下的自動探測
	tests := []struct {
		name     string
		filename string
	}{
		{"html-auto", "www.us-proxy.org.html"},
		{"json-auto", "cdn.jsdelivr.net.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := Helper_loadTestData(tt.filename)
			if body == nil {
				t.Skipf("test data not found: %s", tt.filename)
			}

			proxiesChan := make(chan *proxy.Proxy, 500)
			var extractedCount int64

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range proxiesChan {
					extractedCount++
				}
			}()

			// 不傳遞 URL，測試自動探測
			err := Extractor(proxiesChan, body)
			close(proxiesChan)
			wg.Wait()

			if err != nil {
				t.Errorf("Extractor auto-detect failed: %v", err)
			}

			t.Logf("Auto-detected and extracted %d proxies from %s", extractedCount, tt.filename)
		})
	}
}

func TestExtractByRegex(t *testing.T) {
	testData := []byte(`
		Some text with proxy: 192.168.1.1:8080
		Another proxy 10.0.0.1:3128 here
		HTTP proxy: http://172.16.0.1:8888
		SOCKS5: socks5://192.168.100.1:1080
	`)

	proxiesChan := make(chan *proxy.Proxy, 10)
	var extractedCount int64

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for p := range proxiesChan {
			t.Logf("Extracted: %s:%s (%s)", p.IP, p.Port, p.Protocol)
			extractedCount++
		}
	}()

	count, err := extractByRegex(proxiesChan, testData)
	close(proxiesChan)
	wg.Wait()

	if err != nil {
		t.Errorf("extractByRegex failed: %v", err)
	}

	t.Logf("Regex extracted %d proxies", count)
	if count < 4 {
		t.Errorf("Expected at least 4 proxies, got %d", count)
	}
}

func TestExtractJSONAuto(t *testing.T) {
	// 測試 1: 標準 ip/port 字段
	testData1 := []byte(`
		[
			{"ip": "192.168.1.1", "port": "8080"},
			{"ip": "10.0.0.1", "port": "3128"},
			{"ip": "172.16.0.1", "port": "8888"}
		]
	`)

	proxiesChan := make(chan *proxy.Proxy, 10)
	var extractedCount int64

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range proxiesChan {
			extractedCount++
		}
	}()

	count, err := extractJSONAuto(proxiesChan, testData1)
	close(proxiesChan)
	wg.Wait()

	if err != nil {
		t.Errorf("extractJSONAuto failed: %v", err)
	}

	t.Logf("JSON auto extracted %d proxies", count)
	if count < 3 {
		t.Errorf("Expected at least 3 proxies, got %d", count)
	}
}

func TestLoadTestData(t *testing.T) {
	data := Helper_loadTestData("www.us-proxy.org.html")
	if data == nil {
		t.Errorf("Failed to load test data")
	}
	t.Log("Loaded test data length:", len(data))
}
