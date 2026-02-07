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
func Test_extractProxiesFromHTMLTable(t *testing.T) {
	logrus.SetLevel(logrus.TraceLevel)
	var wg sync.WaitGroup
	proxiesChan := make(chan *proxy.Proxy, 100)
	go func() {
		for p := range proxiesChan {
			t.Logf("Extracted proxy: %s:%s", p.IP, p.Port)

		}
	}()
	wg.Add(1)
	t.Run("www.us-proxy.org", func(t *testing.T) {
		extractProxiesFromHTMLTable(proxiesChan, Helper_loadTestData("www.us-proxy.org.html"))
		wg.Done()
	})

	wg.Wait()
	close(proxiesChan)
}

func Test_extractAndValidateProxies(t *testing.T) {

	logrus.SetLevel(logrus.InfoLevel)
	var wg sync.WaitGroup
	proxiesChan := make(chan *proxy.Proxy, 500)
	go func() {
		for p := range proxiesChan {
			t.Logf("Extracted proxy: %s:%s", p.IP, p.Port)

		}
	}()

	t.Run("proxylist.geonode.com.json", func(t *testing.T) {
		wg.Go(func() {
			extractAndValidateProxies(proxiesChan, Helper_loadTestData("proxylist.geonode.com_01.json"))
			extractAndValidateProxies(proxiesChan, Helper_loadTestData("proxylist.geonode.com_02.json"))
			extractAndValidateProxies(proxiesChan, Helper_loadTestData("proxylist.geonode.com_03.json"))
		})
	})

	t.Run("cdn.jsdelivr.net.json", func(t *testing.T) {
		wg.Go(func() {
			extractAndValidateProxies(proxiesChan, Helper_loadTestData("cdn.jsdelivr.net.json"))
		})
	})

	wg.Wait()
	close(proxiesChan)

}

func Test_loadTestData(t *testing.T) {
	data := Helper_loadTestData("www.us-proxy.org.html")
	if data == nil {
		t.Errorf("Failed to load test data")
	}
	t.Log("Loaded test data length:", len(data))
}
