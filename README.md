# Dynamic Proxy

一個動態代理爬蟲和管理系統，從多個來源自動爬取免費代理 IP，進行健康檢查後提供代理服務。

## 功能特點

- **自動爬取**: 從多個免費代理網站自動爬取代理 IP
- **健康檢查**: 自動驗證代理可用性，支持 HTTP/HTTPS/SOCKS5 協議檢測
- **持久化存儲**: 使用 Badger DB 存儲代理數據
- **定時任務**: 每小時進行健康檢查，每 2 小時爬取新代理
- **代理服務器**: 提供 HTTP/HTTPS 代理服務，支持 CONNECT 方法

## 安裝

```bash
go mod tidy
go build -o dynamic-proxy
```

## 使用方法

### 基本運行（默認模式）
```bash
./dynamic-proxy
```
- 執行一次完整的健康檢查、清理和爬取
- 啟動定時任務（每小時健康檢查、每 2 小時爬取）

### 單次爬取
```bash
./dynamic-proxy -once
```
只執行一次代理爬取，不啟動定時任務。

### 查看代理列表
```bash
./dynamic-proxy -list
```
以 JSON 格式輸出數據庫中所有代理。

### 健康檢查
```bash
./dynamic-proxy -check
```
對所有代理執行健康檢查，標記不可用的代理。

### 清理代理
```bash
./dynamic-proxy -cleanup
```
刪除以下代理：
- 已被禁用的代理
- 沒有更新時間的代理
- 超過 72 小時未更新的代理

### 啟動代理服務器
```bash
./dynamic-proxy -serve :8080
```
在指定端口啟動代理服務器。

### 設置日誌級別
```bash
./dynamic-proxy -log-level debug
```
支持的日誌級別：`debug`, `info`, `warn`, `error`

## 命令行選項

| 選項 | 說明 |
|------|------|
| `-once` | 單次爬取後退出 |
| `-list` | 列出所有代理 |
| `-check` | 執行健康檢查 |
| `-cleanup` | 清理舊代理 |
| `-serve :addr` | 啟動代理服務器 |
| `-log-level level` | 設置日誌級別 |
| `-help` | 顯示幫助信息 |

## 項目結構

```
dynamic-proxy/
├── main.go                 # 主入口
├── internal/
│   ├── proxy/              # 代理核心邏輯
│   │   ├── proxy.go        # Proxy 數據結構、驗證
│   │   ├── proxy_server.go # 代理服務器
│   │   ├── transport.go    # HTTP/SOCKS5 傳輸
│   │   ├── connect_handler.go  # CONNECT 處理
│   │   ├── health_checker.go   # 健康檢查器
│   │   └── helpers.go          # 輔助函數
│   ├── extractor/          # 代理提取邏輯
│   └── fetcher/            # Colly 爬蟲配置
└── proxy_badger_db/        # Badger DB 數據目錄
```

## 代理數據結構

```json
{
  "ip": "192.168.1.1",
  "port": "8080",
  "protocol": "http",
  "disable": false,
  "updated": "2024-01-01T00:00:00Z",
  "count": 100,
  "type": "http",
  "addr": "192.168.1.1:8080",
  "user": "",
  "pass": ""
}
```

## 定時任務

| 時間 | 任務 |
|------|------|
| 每小時 00 分 | 健康檢查 |
| 每小時 30 分 | 清理舊代理 |
| 每 2 小時 00 分 | 爬取新代理 |

## 注意事項

1. 首次運行時會自動創建數據庫目錄
2. 代理數據存儲在 `proxy_badger_db` 目錄
3. 建議定期執行 `-cleanup` 保持數據庫清潔
4. 免費代理穩定性較差，建議配合使用

## License

MIT
