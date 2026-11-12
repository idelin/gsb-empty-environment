# 入侵检测器只保存命中附近的字节，跨流复用缓冲可能把另一租户的秘密附进告警

入侵检测器只保存命中附近的字节，跨流复用缓冲可能把另一租户的秘密附进告警。写供Go探针调用的证据切片包，数据块带可信连接代次、租户和流内偏移，告警引用同一身份下的半开区间；拼接只能填入身份完全相同且内容一致的块，缺口显式标记，不得用相邻内存补齐。对重叠冲突输出双方来源，撤销租户后待发证据全部失效。用小块池反复复用、乱序片段与撤销投递竞争验证每个输出字节的身份链。

## API

```go
store := taskpkg.NewStore()
id, _ := store.OpenConnection("tenant-a") // trusted generation, minted by the store
block, _ := store.Add(taskpkg.Fragment{Identity: id, Offset: 100, Data: probeBuffer})
ev, err := store.Assemble(taskpkg.Alert{Identity: id, Start: 100, End: 120})
```

`Add` copies the fragment immediately, so the probe's small block pool may be
reused as soon as `Add` returns. `Identity` includes both tenant and generation;
blocks from different tenants or generations are never spliced together.

`Assemble` returns a partition of the alert's half-open interval:

- `KindData`: exact bytes, with one or more `Source` entries naming the immutable
  blocks and clipped stream ranges.
- `KindGap`: an explicit hole. `Data` is nil and no adjacent memory is used to
  fill it.
- `KindOverlapConflict`: overlapping blocks disagree. No value wins; `Variants`
  groups the distinct values with all of their sources.

`Evidence.Valid` is dynamic. Calling `store.Revoke(tenant)` discards the
tenant's blocks and invalidates every already-assembled, pending evidence value
for that tenant. `Evidence.Deliver` performs the revocation check and returns a
deep-copied delivery snapshot atomically: after `Revoke` returns, pending
evidence cannot produce a delivery. `Evidence.Verify` checks the full interval
partition and the identity chain of every data byte.
`Evidence.Trace(offset)` and `Evidence.ByteIdentity(offset)` provide per-byte
provenance.
