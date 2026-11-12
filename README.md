# BitTorrent元数据交换投毒防护

BitTorrent对端在扩展握手里给出metadata_size，攻击者报超大尺寸，或发越界、重复的ut_metadata分片。给Go客户端写供对端消息循环调用的元数据交换包：尺寸超过调用方上限拒绝，分片序号不得超过ceil(尺寸/16384)-1，末片长度须精确，收齐后SHA-1须等于info-hash，失败的对端计入封禁。用虚高尺寸、重复分片和哈希不符验证。

## 设计

包 `netsec` 不依赖第三方库，自带严格的最小 bencode 解析器（拒绝前导零、
未排序/重复字典键、尾随字节，并限制嵌套深度）。

- `BanBook`：按 `PeerKey` 累计失败次数，达到阈值（默认 1）即封禁，并发安全。
- `NewExchange`：每个对端一个交换状态机；`metadata_size > maxSize` 或为负时
  立即返回 `ErrMetadataTooLarge` / `ErrInvalidMetadataSize` 并记一次失败。
- `HandleDataPiece`：校验序号范围 `[0, ceil(size/16384)-1]`、每片精确长度
  （末片 = 剩余字节数）、重复分片内容一致性；收齐后做 SHA-1 比对。
- `HandleDataMessage`：消息循环入口，直接喂入剥掉扩展 ID 字节后的
  ut_metadata data 负载（bencode 字典 + 分片字节），内部完成解析与全部校验。
- 任何校验失败都会 `Strike` 对端并把该对端交换置为 failed，后续消息一律拒绝。

## 消息循环用法

```go
book := netsec.NewBanBook(1) // 一次校验失败即封禁

// 扩展握手到达时
size, err := netsec.ParseExtendedHandshake(handshakePayload)
if err != nil { /* 记录并断开 */ }
ex, err := netsec.NewExchange(peerKey, book, size, maxAllowedSize, infoHash)
if err != nil {
    // 虚高尺寸等：book.Banned(peerKey) 已可判定
    return
}

// 后续 ut_metadata data 消息（已剥掉扩展消息 ID 字节）
complete, err := ex.HandleDataMessage(payload)
if err != nil {
    disconnect(peerKey) // book 已累计失败，Banned() 决定是否拉黑
    return
}
if complete {
    infoDict := ex.Metadata() // SHA-1 已与 info-hash 校验一致
}
```

## 验证

`go test -race ./...` 覆盖：虚高 `metadata_size`、负尺寸、越界/负分片序号、
末片长度不精确、一致与冲突的重复分片、SHA-1 不符、`total_size` 漂移、
畸形 bencode、封禁阈值/重置以及并发投递竞态。
