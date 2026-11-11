# sitemapdisc

Go 爬虫调度器的站点地图发现组件。

## 能力

- 递归处理嵌套 `sitemapindex`，用工作队列迭代遍历，避免深层递归。
- 通过 gzip 魔数（`1f 8b`，不依赖文件名或 `Content-Type`）识别并解压内容；传输层透明解压同样受限。
- 单文件条目上限 `DefaultMaxEntries = 50000`，解压体积上限 `DefaultMaxDecompressed = 50 MiB`，超限分别返回 `ErrTooManyEntries` / `ErrTooLarge`。
- 跨文件对页面 URL 去重，并以规范化 URL 为键检测 sitemap 循环引用，循环只跳过不报错。
- `lastmod` 支持 W3C Datetime 全套格式：带偏移/`Z` 的完整时间戳、小数秒、日期时间、仅日期、仅年月、仅年份；无法解析时保留原始字符串。
- 子地址（含相对引用，先按父 sitemap 解析）必须与父 sitemap 同 scheme 类（http/https）、同主机名与端口，且路径位于父 sitemap 所在目录下，否则静默拒绝。

## 用法

```go
d := sitemapdisc.New()
d.UserAgent = "my-crawler/1.0"
entries, err := d.Discover(ctx, "https://example.com/sitemaps/index.xml")
```

`Discover` 返回去重后的 `[]Entry`，每项包含规范化后的绝对 `Loc`、解析出的 `LastMod`（零值表示缺失或无法解析）与原始 `LastModRaw`。抓取、解析或限制类错误会立即中止并返回，可用 `errors.Is` 匹配哨兵错误。

## 测试

`sitemapdisc_test.go` 用内存 HTTP 服务器（`net.Pipe` 驱动完整 `net/http` 栈）覆盖：嵌套索引与 gzip、跨文件去重、循环引用、条目超限、解压超限（gzip 高压缩比炸弹）、越界地址（异主机、异端口、目录逃逸、非 http scheme）、非法种子与 404，以及 `lastmod` 解析和作用域单元测试。

```sh
go test ./...
```
