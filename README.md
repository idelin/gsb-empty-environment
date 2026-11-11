# cdcupload

采集服务反复上传大文件小改动版本的可恢复上传组件（Go，仅标准库）。

## 设计

- **内容定义分块（CDC）**：64 位 Gear 滚动哈希 `rolling = (rolling << 1) + gear[b]`，
  配合 FastCDC 风格归一化掩码确定切点。Gear 常量表用 splitmix64 从固定种子确定性生成。
- **块长可配置**：`ChunkConfig{MinSize, AvgSize, MaxSize}`。
  - 小于 `MinSize` 抑制切点；
  - `MinSize..AvgSize` 使用更窄掩码（更易切出），把偏长的块拉回均值；
  - `AvgSize..MaxSize` 使用由 `AvgSize` 推导的标准掩码；
  - 到达 `MaxSize` 强制切点，保证硬上界。
  切点由内容决定，插入/删除字节只改变编辑点附近的块，前缀与后缀块原样复用。
- **内容寻址去重**：每个块按 SHA-256 摘要命名；上传前先查远端 `HasChunk`，
  同一文件重传或新版本文件之间天然去重。
- **可恢复**：每成功上传一个块就把会话状态（偏移、长度、摘要）原子落盘。
  中断后再次调用 `UploadFile` 会重新分块、跳过已完成的块，仅传输缺失块；
  源文件若在中断后被改动（大小或全文件摘要不一致）返回 `ErrSourceChanged`。
- **清单最后原子提交**：所有块到位后才写 manifest，`临时文件 + fsync + rename`
  原子发布，读取方永远看不到半成品；块与 manifest 的写操作均幂等，可安全重试。

## 主要 API

```go
chunker, _ := cdcupload.NewChunker(reader, cdcupload.DefaultChunkConfig())
for {
    chunk, payload, err := chunker.NextChunk() // 含块摘要与载荷
    if err == io.EOF { break }
    _ = chunk
}

store, _ := cdcupload.NewRemoteStore("/var/lib/upload-remote")
manifest, err := cdcupload.UploadFile(ctx, store, stateDir, "/data/big.log",
    cdcupload.DefaultChunkConfig())

var out bytes.Buffer
_ = cdcupload.Restore(ctx, store, manifest, &out) // 逐块校验 + 全文件哈希校验
```

`Uploader` 是传输接口（`HasChunk` / `PutChunk` / `CommitManifest`），
`RemoteStore` 是基于本地文件系统的内容寻址参考实现，可替换为真实对象存储客户端。

## 测试

```sh
go test -v -count=1 ./...
go test -race -cover ./...
```

覆盖内容：

- 块长上下界、分块确定性、全文件摘要；
- 随机位置插入/删除下的块复用率（阈值 ≥ 0.9）与局部性（单次编辑最多扰动 4 个块）；
- 多版本文件之间的复用率与去重传输量统计；
- 模拟多次传输崩溃后的断点恢复，恢复结果逐字节一致；
- 空文件/小文件、源文件变更检测、manifest 原子提交与无临时文件残留。

采集服务反复上传大文件的小改动版本。给Go采集服务实现基于内容分块的上传组件：用Gear滚动哈希和归一化分块确定切点，最小、平均、最大块长可配置，插入或删除字节只影响附近的块；块按摘要去重，上传中断后可恢复，清单最后原子提交。在随机位置插入数据，测试块复用率和恢复后的完整性。
