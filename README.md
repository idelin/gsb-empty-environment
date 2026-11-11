# rangedl

采集SDK下载大文件时网络常中断，服务端文件也可能中途更新。给Go采集SDK实现可恢复下载组件：并发按区间请求，用If-Range和ETag保证各段来自同一版本，服务端忽略Range返回200时正确退化，各段完成情况原子落盘；文件版本变化时丢弃已下载部分重新开始，最后校验长度和摘要。用模拟服务器注入断连、版本变化和错误的Content-Range。

## 用法

```go
dl := rangedl.NewDownloader(rangedl.Config{
    Concurrency:    4,
    ChunkSize:      8 << 20,
    ExpectedSize:   size,          // 可选
    ExpectedSHA256: hexDigest,     // 可选
})
res, err := dl.Download(ctx, url, "/var/lib/collector/data.log")
```

中断后用相同目标路径再次调用 `Download` 即可续传：进度保存在
`<dest>.part`（数据）与 `<dest>.part.json`（状态），成功后原子改名为
`<dest>` 并删除状态文件。

## 设计要点

- **版本锚定**：先用 1 字节探测请求获取 `ETag`/`Last-Modified` 与总大小，
  之后每个分片请求都带 `If-Range`；分片返回的 `ETag` 不一致或收到
  `412 Precondition Failed` 即判定版本变化，删除 `.part` 与状态文件后整体重启
  （`MaxRestarts` 次，指数退避）。
- **并发分片**：固定大小区间 + 有界 worker 池，通过 `WriteAt` 写绝对偏移，
  各分片互不阻塞。
- **原子落盘**：每完成一个分片先 `fsync` 数据，再以
  「临时文件 + fsync + rename + 目录 fsync」原子更新 JSON 状态，崩溃后
  已标记的分片不会重复请求、未标记的分片不会丢数据。
- **Range 退化**：探测或分片请求收到 `200` 时切换为普通 GET 全量下载，
  截断 `.part` 从头写入，并校验 `Content-Length`。
- **错误 Content-Range**：起始偏移/总大小与请求不符、或实际字节数不足时
  返回 `ErrContentRange`（不可重试，避免写入错误版本数据）。
- **最终校验**：落盘前校验总长度（含可选 `ExpectedSize`）与 SHA-256
  （含可选 `ExpectedSHA256`），失败则清理半成品并返回 `ErrSize`/`ErrChecksum`。

## 测试

`rangedl_test.go` 内置基于 `net.Pipe` 的模拟服务器（沙箱禁止监听端口，
因此不使用 TCP），可注入：

- 中途断连（Hijack 后直接关闭连接），验证重试与断点续传；
- 下载中切换文件版本，验证 `If-Range`/412 触发的整体重启；
- 伪造 `Content-Range`，验证致命错误与状态不被污染；
- 忽略 `Range` 始终回 200，验证退化全量下载；
- 500 等可重试状态码与最终长度/摘要不匹配。

```sh
go test -race -count=1 ./...
```
