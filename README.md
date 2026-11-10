# framingcheck

离线复现老网关与源站对 HTTP/1.1 请求边界理解不一致（请求走私）的命令行工具。
工具从 stdin 读取原始 TCP 字节流，按两套策略连续解析流水线请求，并在首个边界
分歧处输出一行 JSON；所有请求边界完全一致时输出 `OK`。

## 构建与运行

```sh
go build -buildvcs=false -o framingcheck .
cat capture.bin | ./framingcheck
```

输出字段：

- `start`：请求在流中的起始偏移，从 0 开始。
- `gatewayEnd` / `originEnd`：两端各自认定的请求末端（不含该偏移）。
- 任一端语法错误或遇到截断时，对应末端为 `-1`。
- 两端同时出错也会输出记录并停止；没有分歧时输出 `OK`。

## 策略与规则

- 网关优先使用 `Content-Length`，源站优先使用 `Transfer-Encoding: chunked`；
  缺少首选头时回退到另一个；两者都没有则正文为空。
- 头名比较忽略大小写。
- `Content-Length`：重复头或逗号分隔的多个十进制数值必须全部相等，否则为语法
  错误；非十进制数值同样无效。
- `Transfer-Encoding`：只接受单个值 `chunked`（大小写不敏感），多个头、逗号
  列表或其他编码均为语法错误。
- 两端都会先验证全部头，再选择正文策略。
- chunked 支持块扩展（分号后内容忽略）、十六进制块大小、trailer，零块之后以
  空行结束；trailer 也必须是合法头字段。
- 行结束接受 CRLF，也兼容裸 LF；chunk 数据后的分隔仍严格要求 CRLF。

## 内存模型

输入按固定大小块分页，正文用丢弃式扫描，不缓存正文内容。每个解析游标独立记录
绝对偏移；只有仍被某个游标所在块引用的数据块会保留，因此常驻内存只取决于两端
解析进度之差，与整个流或单个正文的大小无关。正文允许包含任意字节（包括 NUL）。

## 测试

```sh
go test -buildvcs=false -race ./...
```

覆盖正常流水线、逐字节跨读取分块、冲突 `Content-Length`、CL.TE 真实边界分歧、
截断、chunked 扩展与 trailer、头大小写、单边错误、JSON 结构、二进制正文以及
大流下的内存有界性。
