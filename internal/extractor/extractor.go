package extractor

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/PuerkitoBio/goquery"
	"github.com/e2u/dynamic-proxy/internal/proxy"
	"github.com/sirupsen/logrus"
)

// ExtractRule 提取規則定義
type ExtractRule struct {
	Name          string   // 規則名稱
	MatchURL      string   // URL 匹配關鍵字（空表示通用）
	ContentType   string   // 內容類型：html, json, auto
	TableSelector string   // HTML 表格選擇器
	IPSelector    string   // IP 專用選擇器（可選）
	PortSelector  string   // Port 專用選擇器（可選）
	IPFields      []string // JSON IP 字段名（支持多個別名）
	PortFields    []string // JSON Port 字段名（支持多個別名）
	ArrayPath     string   // JSON 數組路徑（空表示根節點或自動探測）
}

// 預定義規則庫
var extractRules = []ExtractRule{
	// free-proxy-list.net 系列
	{
		Name:          "free-proxy-list-main",
		MatchURL:      "free-proxy-list.net/en/",
		ContentType:   "html",
		TableSelector: "table tbody tr",
		IPFields:      []string{},
		PortFields:    []string{},
	},
	{
		Name:          "free-proxy-list-socks",
		MatchURL:      "free-proxy-list.net/en/socks-proxy.html",
		ContentType:   "html",
		TableSelector: "table tbody tr",
		IPFields:      []string{},
		PortFields:    []string{},
	},
	{
		Name:          "free-proxy-list-ssl",
		MatchURL:      "free-proxy-list.net/en/ssl-proxy.html",
		ContentType:   "html",
		TableSelector: "table tbody tr",
		IPFields:      []string{},
		PortFields:    []string{},
	},
	{
		Name:          "free-proxy-list-anonymous",
		MatchURL:      "free-proxy-list.net/en/anonymous-proxy.html",
		ContentType:   "html",
		TableSelector: "table tbody tr",
		IPFields:      []string{},
		PortFields:    []string{},
	},
	{
		Name:          "free-proxy-list-uk",
		MatchURL:      "free-proxy-list.net/en/uk-proxy.html",
		ContentType:   "html",
		TableSelector: "table tbody tr",
		IPFields:      []string{},
		PortFields:    []string{},
	},
	{
		Name:          "free-proxy-list-google",
		MatchURL:      "free-proxy-list.net/en/google-proxy.html",
		ContentType:   "html",
		TableSelector: "table tbody tr",
		IPFields:      []string{},
		PortFields:    []string{},
	},
	// proxyscrape
	{
		Name:        "proxyscrape",
		MatchURL:    "proxyscrape.com",
		ContentType: "json",
		ArrayPath:   "proxies",
		IPFields:    []string{"ip", "address", "host"},
		PortFields:  []string{"port", "proxy_port"},
	},
	// proxylist.geonode.com
	{
		Name:        "geonode",
		MatchURL:    "proxylist.geonode.com",
		ContentType: "json",
		IPFields:    []string{"ip", "address", "host", "proxy_ip"},
		PortFields:  []string{"port", "proxy_port", "proxyport"},
	},
	// cdn.jsdelivr.net (GitHub proxy lists)
	{
		Name:        "jsdelivr",
		MatchURL:    "cdn.jsdelivr.net",
		ContentType: "json",
		IPFields:    []string{"ip", "proxy_ip", "address", "host", "server"},
		PortFields:  []string{"port", "proxy_port", "proxyport"},
	},
	// 通用 JSON 規則（自動探測字段）
	{
		Name:        "generic-json",
		MatchURL:    "",
		ContentType: "json",
		IPFields:    []string{"ip", "proxy_ip", "address", "host", "server", "ip_address"},
		PortFields:  []string{"port", "proxy_port", "proxyport", "port_number"},
	},
	// 通用 HTML 規則（自動探測表格）
	{
		Name:          "generic-html",
		MatchURL:      "",
		ContentType:   "html",
		TableSelector: "", // 自動探測
		IPFields:      []string{},
		PortFields:    []string{},
	},
}

// 預編譯正則
var (
	// 正則 1: protocol://ip:port
	regexProtocol = regexp.MustCompile(
		`(?i)(?:(?P<protocol>socks[45a]?|http|https)://)?` +
			`(?P<ip>(?:25[0-5]|2[0-4]\d|[01]?\d{1,2})\.(?:25[0-5]|2[0-4]\d|[01]?\d{1,2})\.(?:25[0-5]|2[0-4]\d|[01]?\d{1,2})\.(?:25[0-5]|2[0-4]\d|[01]?\d{1,2})):` +
			`(?P<port>\d{1,5})`)

	// 正則 2: ip:port (各種分隔符)
	regexIPPort = regexp.MustCompile(
		`((?:25[0-5]|2[0-4]\d|[01]?\d{1,2})\.(?:25[0-5]|2[0-4]\d|[01]?\d{1,2})\.(?:25[0-5]|2[0-4]\d|[01]?\d{1,2})\.(?:25[0-5]|2[0-4]\d|[01]?\d{1,2}))` +
			`[:\s,;\|\(\[\{]+` +
			`(\d{1,5})(?:\D|$)`)

	// 正則 3: JSON 格式 "ip":"x.x.x.x"..."port":"1234"
	regexJSON = regexp.MustCompile(
		`(?i)\s*"\s*(?:ip|proxy_ip|address|host)\s*"\s*:\s*"\s*((?:\d{1,3}\.){3}\d{1,3})\s*"[\s\S]*?"\s*(?:port|proxy_port)\s*"\s*:\s*"\s*(\d+)\s*"`)

	// IP 驗證正則
	regexIP = regexp.MustCompile(`^(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})$`)
	// Port 驗證正則
	regexPort = regexp.MustCompile(`^(\d{2,5})$`)
)

// Extractor 主提取函數（自適應選擇提取策略）
func Extractor(proxiesChan chan<- *proxy.Proxy, body []byte, url ...string) error {
	logrus.Debugf("extractor called, body length: %d", len(body))

	targetURL := ""
	if len(url) > 0 {
		targetURL = url[0]
	}

	// 1. 嘗試匹配預定義規則
	for _, rule := range extractRules {
		if rule.MatchURL != "" && targetURL != "" && !strings.Contains(targetURL, rule.MatchURL) {
			continue
		}

		var count int64
		var err error

		switch rule.ContentType {
		case "json":
			count, err = extractFromJSONWithRule(proxiesChan, body, rule)
		case "html":
			count, err = extractFromHTMLWithRule(proxiesChan, body, rule)
		default:
			// auto: 自動檢測
			if isJSON(body) {
				count, err = extractFromJSONWithRule(proxiesChan, body, rule)
			} else if isHTML(body) {
				count, err = extractFromHTMLWithRule(proxiesChan, body, rule)
			}
		}

		if err == nil && count > 0 {
			logrus.Infof("extractor succeeded with rule '%s', found %d proxies", rule.Name, count)
			return nil
		}
	}

	// 2. 規則都失敗，使用 fallback 策略
	logrus.Info("no rule matched, using fallback extractors")

	// Fallback 1: JSON 自動探測
	if isJSON(body) {
		count, err := extractJSONAuto(proxiesChan, body)
		if err == nil && count > 0 {
			logrus.Infof("fallback JSON auto-extraction succeeded, found %d proxies", count)
			return nil
		}
	}

	// Fallback 2: HTML 自動探測
	if isHTML(body) {
		count, err := extractHTMLAuto(proxiesChan, body)
		if err == nil && count > 0 {
			logrus.Infof("fallback HTML auto-extraction succeeded, found %d proxies", count)
			return nil
		}
	}

	// Fallback 3: 正則提取
	count, err := extractByRegex(proxiesChan, body)
	if err == nil && count > 0 {
		logrus.Infof("fallback regex extraction succeeded, found %d proxies", count)
		return nil
	}

	logrus.Warn("all extraction methods failed")
	return nil
}

// isJSON 檢測是否為 JSON 格式
func isJSON(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

// isHTML 檢測是否為 HTML 格式
func isHTML(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	return strings.HasPrefix(trimmed, "<") &&
		(strings.Contains(trimmed, "</") || strings.Contains(trimmed, "/>"))
}

// extractFromJSONWithRule 使用規則從 JSON 提取
func extractFromJSONWithRule(proxiesChan chan<- *proxy.Proxy, body []byte, rule ExtractRule) (int64, error) {
	logrus.Debugf("extractFromJSONWithRule: rule=%s", rule.Name)

	var totalProxyCount int64
	seen := make(map[string]bool)

	// 解析 JSON
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		logrus.Errorf("failed to parse JSON: %v", err)
		return 0, err
	}

	// 如果有 ArrayPath，先定位到數組
	if rule.ArrayPath != "" {
		data = getJSONPath(data, rule.ArrayPath)
		if data == nil {
			logrus.Warnf("array path '%s' not found in JSON", rule.ArrayPath)
			return 0, nil
		}
	}

	// 遍歷數組提取代理
	extractJSONArray(proxiesChan, data, rule.IPFields, rule.PortFields, seen, &totalProxyCount)

	return totalProxyCount, nil
}

// getJSONPath 獲取 JSON 路徑
func getJSONPath(data any, path string) any {
	if path == "" {
		return data
	}

	parts := strings.Split(path, ".")
	current := data

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			current = v[part]
		case []any:
			// 數組索引
			if idx, err := strconv.Atoi(part); err == nil && idx < len(v) {
				current = v[idx]
			} else {
				// 遍歷數組找匹配
				for _, item := range v {
					if m, ok := item.(map[string]any); ok {
						if val, exists := m[part]; exists {
							current = val
							break
						}
					}
				}
			}
		default:
			return nil
		}
		if current == nil {
			return nil
		}
	}

	return current
}

// extractJSONArray 從 JSON 數組提取
func extractJSONArray(proxiesChan chan<- *proxy.Proxy, data any, ipFields, portFields []string, seen map[string]bool, count *int64) {
	arr, ok := data.([]any)
	if !ok {
		// 嘗試當單個對象處理
		extractProxyFromObject(proxiesChan, data, ipFields, portFields, seen, count)
		return
	}

	for _, item := range arr {
		// 檢查是否係嵌套數組
		if nestedArr, ok := item.([]any); ok {
			extractJSONArray(proxiesChan, nestedArr, ipFields, portFields, seen, count)
			continue
		}

		// 檢查是否係嵌套對象（需要遞歸）
		if nestedObj, ok := item.(map[string]any); ok {
			// 檢查呢個對象本身是否包含代理
			if extractProxyFromObject(proxiesChan, nestedObj, ipFields, portFields, seen, count) > 0 {
				continue
			}
			// 否則遞歸搜尋子字段
			for _, v := range nestedObj {
				extractJSONArray(proxiesChan, v, ipFields, portFields, seen, count)
			}
			continue
		}
	}
}

// extractProxyFromObject 從單個 JSON 對象提取代理
func extractProxyFromObject(proxiesChan chan<- *proxy.Proxy, obj any, ipFields, portFields []string, seen map[string]bool, count *int64) int64 {
	m, ok := obj.(map[string]any)
	if !ok {
		return 0
	}

	// 嘗試多個 IP 字段名
	var ip, port string
	for _, field := range ipFields {
		if val, exists := m[field]; exists {
			if s, ok := val.(string); ok {
				ip = s
				break
			}
			// 處理數字類型
			if n, ok := val.(float64); ok {
				ip = strconv.FormatFloat(n, 'f', 0, 64)
				break
			}
		}
	}

	for _, field := range portFields {
		if val, exists := m[field]; exists {
			if s, ok := val.(string); ok {
				port = s
				break
			}
			// 處理數字類型
			if n, ok := val.(float64); ok {
				port = strconv.FormatFloat(n, 'f', 0, 64)
				break
			}
		}
	}

	if ip == "" || port == "" {
		return 0
	}

	key := ip + ":" + port
	if seen[key] {
		return 0
	}
	seen[key] = true

	// 驗證並發送
	if isValidIP(ip) && isValidPort(port) {
		p := &proxy.Proxy{
			IP:       ip,
			Port:     port,
			Protocol: "http",
			Addr:     ip + ":" + port,
		}
		proxiesChan <- p
		atomic.AddInt64(count, 1)
		return 1
	}

	return 0
}

// extractJSONAuto JSON 自動探測提取
func extractJSONAuto(proxiesChan chan<- *proxy.Proxy, body []byte) (int64, error) {
	logrus.Debug("extractJSONAuto: starting auto-detection")

	var totalProxyCount int64
	seen := make(map[string]bool)

	// 常見字段名組合
	fieldCombos := []struct {
		ipFields   []string
		portFields []string
	}{
		{[]string{"ip"}, []string{"port"}},
		{[]string{"proxy_ip"}, []string{"proxy_port"}},
		{[]string{"address"}, []string{"port"}},
		{[]string{"host"}, []string{"port"}},
		{[]string{"server"}, []string{"port"}},
		{[]string{"ip_address"}, []string{"port"}},
		{[]string{"ip", "proxy_ip", "address", "host"}, []string{"port", "proxy_port"}},
	}

	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, err
	}

	// 嘗試每種字段組合
	for _, combo := range fieldCombos {
		extractJSONArray(proxiesChan, data, combo.ipFields, combo.portFields, seen, &totalProxyCount)
		if totalProxyCount > 0 {
			logrus.Debugf("extractJSONAuto: matched with fields ip=%v, port=%v", combo.ipFields, combo.portFields)
			break
		}
	}

	// 如果仲係無，嘗試遞歸搜尋
	if totalProxyCount == 0 {
		searchJSONRecursive(data, proxiesChan, seen, &totalProxyCount)
	}

	return totalProxyCount, nil
}

// searchJSONRecursive 遞歸搜尋 JSON 中嘅代理
func searchJSONRecursive(data any, proxiesChan chan<- *proxy.Proxy, seen map[string]bool, count *int64) {
	switch v := data.(type) {
	case map[string]any:
		// 嘗試從呢個對象提取
		ipFields := []string{"ip", "proxy_ip", "address", "host", "server"}
		portFields := []string{"port", "proxy_port", "proxyport"}
		extractProxyFromObject(proxiesChan, v, ipFields, portFields, seen, count)

		// 遞歸搜尋子字段
		for _, val := range v {
			searchJSONRecursive(val, proxiesChan, seen, count)
		}
	case []any:
		for _, item := range v {
			searchJSONRecursive(item, proxiesChan, seen, count)
		}
	}
}

// extractFromHTMLWithRule 使用規則從 HTML 提取
func extractFromHTMLWithRule(proxiesChan chan<- *proxy.Proxy, body []byte, rule ExtractRule) (int64, error) {
	logrus.Debugf("extractFromHTMLWithRule: rule=%s", rule.Name)

	var totalProxyCount int64

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return 0, err
	}

	// 清理無用標籤
	doc.Find("script, style, noscript, iframe, head, meta, link, textarea, nav").Remove()

	selector := rule.TableSelector
	if selector == "" {
		selector = "tr"
	}

	doc.Find(selector).Each(func(i int, tr *goquery.Selection) {
		var ip, port string

		// 如果有專用選擇器，用專用選擇器
		if rule.IPSelector != "" {
			ip = strings.TrimSpace(tr.Find(rule.IPSelector).First().Text())
		}
		if rule.PortSelector != "" {
			port = strings.TrimSpace(tr.Find(rule.PortSelector).First().Text())
		}

		// 如果無專用選擇器，遍歷所有 td
		if ip == "" || port == "" {
			tr.Find("td, div, span").Each(func(j int, td *goquery.Selection) {
				text := strings.TrimSpace(td.Text())
				if ip == "" && regexIP.MatchString(text) {
					ip = text
				} else if ip != "" && port == "" && regexPort.MatchString(text) {
					port = text
				}
			})
		}

		if isValidIP(ip) && isValidPort(port) {
			key := ip + ":" + port
			if !seenProxy(key) {
				p := &proxy.Proxy{
					IP:       ip,
					Port:     port,
					Protocol: "http",
					Addr:     ip + ":" + port,
				}
				proxiesChan <- p
				atomic.AddInt64(&totalProxyCount, 1)
			}
		}
	})

	return totalProxyCount, nil
}

// extractHTMLAuto HTML 自動探測提取
func extractHTMLAuto(proxiesChan chan<- *proxy.Proxy, body []byte) (int64, error) {
	logrus.Debug("extractHTMLAuto: starting auto-detection")

	var totalProxyCount int64

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return 0, err
	}

	// 清理無用標籤
	doc.Find("script, style, noscript, iframe, head, meta, link, textarea, nav").Remove()

	// 嘗試多種選擇器
	selectors := []string{
		"tr",
		"table tr",
		"tbody tr",
		"[class*='proxy'] tr",
		"[id*='proxy'] tr",
		".proxy-list tr",
		"ul li",
		"[class*='list'] li",
	}

	seen := make(map[string]bool)

	for _, selector := range selectors {
		doc.Find(selector).Each(func(i int, s *goquery.Selection) {
			var ip, port string

			s.Find("td, div, span, a").Each(func(j int, td *goquery.Selection) {
				text := strings.TrimSpace(td.Text())
				if ip == "" && regexIP.MatchString(text) {
					ip = text
				} else if ip != "" && port == "" && regexPort.MatchString(text) {
					port = text
				}
			})

			if isValidIP(ip) && isValidPort(port) {
				key := ip + ":" + port
				if !seen[key] {
					seen[key] = true
					p := &proxy.Proxy{
						IP:       ip,
						Port:     port,
						Protocol: "http",
						Addr:     ip + ":" + port,
					}
					proxiesChan <- p
					atomic.AddInt64(&totalProxyCount, 1)
				}
			}
		})

		if totalProxyCount > 10 { // 找到足夠多就停止
			return totalProxyCount, nil
		}
	}

	return totalProxyCount, nil
}

// extractByRegex 正則提取（最後防線）
func extractByRegex(proxiesChan chan<- *proxy.Proxy, body []byte) (int64, error) {
	logrus.Debug("extractByRegex: starting regex extraction")

	var totalProxyCount int64
	seen := make(map[string]bool)
	bodyStr := string(body)

	// 正則 1: protocol://ip:port
	matches1 := regexProtocol.FindAllStringSubmatch(bodyStr, -1)
	names1 := regexProtocol.SubexpNames()

	for _, match := range matches1 {
		result := make(map[string]string)
		for i, name := range names1 {
			if i != 0 && name != "" {
				result[name] = match[i]
			}
		}

		if !isValidPort(result["port"]) {
			continue
		}

		key := result["ip"] + ":" + result["port"]
		if seen[key] {
			continue
		}
		seen[key] = true

		protocol := strings.ToLower(result["protocol"])
		if protocol == "" {
			protocol = "http"
		}

		p := &proxy.Proxy{
			IP:       result["ip"],
			Port:     result["port"],
			Protocol: protocol,
			Addr:     result["ip"] + ":" + result["port"],
		}
		proxiesChan <- p
		atomic.AddInt64(&totalProxyCount, 1)
	}

	// 正則 2: ip:port (各種分隔符)
	matches2 := regexIPPort.FindAllStringSubmatch(bodyStr, -1)
	for _, m := range matches2 {
		if len(m) < 3 {
			continue
		}

		if !isValidPort(m[2]) {
			continue
		}

		key := m[1] + ":" + m[2]
		if seen[key] {
			continue
		}
		seen[key] = true

		p := &proxy.Proxy{
			IP:       m[1],
			Port:     m[2],
			Protocol: "http",
			Addr:     m[1] + ":" + m[2],
		}
		proxiesChan <- p
		atomic.AddInt64(&totalProxyCount, 1)
	}

	// 正則 3: JSON 格式
	matches3 := regexJSON.FindAllStringSubmatch(bodyStr, -1)
	for _, m := range matches3 {
		if len(m) < 3 {
			continue
		}

		if !isValidPort(m[2]) {
			continue
		}

		key := m[1] + ":" + m[2]
		if seen[key] {
			continue
		}
		seen[key] = true

		p := &proxy.Proxy{
			IP:       m[1],
			Port:     m[2],
			Protocol: "http",
			Addr:     m[1] + ":" + m[2],
		}
		proxiesChan <- p
		atomic.AddInt64(&totalProxyCount, 1)
	}

	return totalProxyCount, nil
}

// isValidIP 驗證 IP 地址
func isValidIP(ip string) bool {
	if ip == "" {
		return false
	}
	if !regexIP.MatchString(ip) {
		return false
	}
	// 檢查每個段
	parts := strings.Split(ip, ".")
	for _, part := range parts {
		num, err := strconv.Atoi(part)
		if err != nil || num < 0 || num > 255 {
			return false
		}
	}
	return true
}

// isValidPort 驗證端口
func isValidPort(port string) bool {
	if port == "" {
		return false
	}
	num, err := strconv.Atoi(port)
	return err == nil && num > 0 && num <= 65535
}

// seenProxy 檢查代理是否已經見過（使用局部 seen map 更好，呢度係簡單實現）
var globalSeen sync.Map

func seenProxy(key string) bool {
	_, loaded := globalSeen.LoadOrStore(key, true)
	return loaded
}

// ResetSeenMap 重置已見記錄（每次爬取前調用）
func ResetSeenMap() {
	globalSeen = sync.Map{}
}
