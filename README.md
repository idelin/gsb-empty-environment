# warcwriter

归档爬虫需要以WARC格式保存抓取结果。给Go爬虫实现WARC写入器：每条记录单独作为一个gzip成员，按WARC/1.1写request、response、metadata记录，Content-Length和块摘要正确；载荷摘要重复时改写为revisit记录，相关记录用WARC-Concurrent-To关联；文件按大小滚动并生成CDXJ索引。实现读取路径，按索引偏移随机读取记录并校验摘要。

## 实现

纯标准库（`compress/gzip`、`crypto/sha1`、`net/http`）实现，无外部依赖。

- `record.go`：WARC/1.1 记录序列化，`Content-Length`、`WARC-Block-Digest`（块摘要，`sha1:` + RFC 4648 base32）、`WARC-Payload-Digest`（载荷摘要）。
- `writer.go`：每条记录用独立 `gzip.Writer` 写成单独 gzip 成员；每次抓取写 request、response（或 revisit）、metadata 三类记录，response/metadata 通过 `WARC-Concurrent-To` 关联到 request。载荷摘要重复时改写为 `revisit`（`identical-payload-digest` profile，`WARC-Refers-To` 指向原始 response），块中只保留 HTTP 头。文件按 `MaxFileBytes` 滚动，命名 `<prefix>-NNNNN-<timestamp>.warc.gz`，每个文件首条为 warcinfo。
- `cdxj.go`：CDXJ 索引（`<SURT> <14位时间戳> <JSON>`），记录 response/revisit 的文件名、gzip 成员偏移与长度、摘要、record id 等；重开 Writer 时从索引恢复去重表。
- `reader.go`：按索引 `offset` 随机读取单个 gzip 成员，校验 `Content-Length`、块摘要与载荷摘要，摘要不符返回 `DigestMismatchError`。
- `httpbody.go`：解码 HTTP 实体（去除 chunked 等传输编码），载荷摘要基于解码后的实体。
- `gzip_members.go`：用 `Multistream(false)` 正确切分多成员 gzip，保证按偏移读取。

## 用法

```go
w, err := warcwriter.NewWriter(warcwriter.Options{
    Dir:          "./archive",
    Prefix:       "crawl",
    MaxFileBytes: 100 << 20, // 100 MiB
})
defer w.Close()

res, err := w.WriteCapture(warcwriter.Capture{
    Request:  rawRequestBytes,
    Response: rawResponseBytes,
    Metadata: map[string]string{"crawler": "mybot"},
})

entries, _ := warcwriter.ReadCDXJ("./archive/crawl.cdxj")
r := warcwriter.NewReader("./archive")
rec, err := r.Open(entries[0]) // 按偏移随机读取并校验摘要
```

## 测试

```sh
go test -race ./...
```
