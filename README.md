# robotstxt

按 RFC 9309 实现的 Go 爬虫准入（robots.txt）包。

## 行为

- 组选择：选择最具体匹配的 User-agent 组（最长的大小写不敏感子串匹配），同名组合并；无匹配时回退到 `*` 组。
- 规则裁决：Allow 与 Disallow 按最长匹配优先，长度相同 Allow 优先；支持 `*` 通配符与结尾 `$` 锚点。
- 百分号编码：含 `%XX` 的规则解码后与解码路径比较，未编码规则与原始路径比较，两种形式不交叉匹配；查询串不参与匹配。
- 抓取：响应体超过 500 KiB（`MaxBodySize`）截断后解析；重定向最多跟随 5 次。
- 状态码：2xx 解析正文；4xx 视为不存在 robots.txt（全部允许）；5xx 与网络错误视为全部禁止。
- 缓存：按来源（scheme://host[:port]）缓存结果，使用 `TTL`（默认 `DefaultCacheTTL` = 24h）控制有效期，并合并同来源并发请求。

## 用法

```go
package main

import (
	"context"
	"log"

	"example.org/robotstxt"
)

func main() {
	c := &robotstxt.Client{
		UserAgent: "mybot/1.0",
		TTL:       robotstxt.DefaultCacheTTL,
	}

	allowed, err := c.Allowed(context.Background(), "https://example.com/private/data")
	if err != nil {
		// 网络错误：allowed 为 false，err 非空。
		log.Printf("robots check failed: %v", err)
	}
	if !allowed {
		log.Print("blocked by robots.txt")
	}
}
```

`Allowed` 接受 `string` 或 `*url.URL`；非 HTTP(S) 的 URL 一律允许。零值 `Client` 可直接使用。

## 测试

`go test ./...` 覆盖 RFC 9309 规范示例（前缀匹配、`$`、`*`、最具体组、同名组合并、等长 Allow 优先）、百分号编码路径，以及 4xx/5xx/网络错误、500 KiB 截断和 TTL 缓存行为。
