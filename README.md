# 共享内存序列锁可信读取

`netsec` 包供 Go 采集宿主从低信任进程维护的共享页中读取变长记录。
读取器不持有锁，依靠 seqlock 协议在并发写入下只接受完整一致的快照。

## 接受条件

一条记录只有同时满足以下条件才会被 `Reader.Read` 返回：

1. 采样窗口两端读到的 `(gen, seq)` 完全相同，且 `seq` 为偶数
   （奇数表示低信任方正在写入，直接重试）；
2. 紧邻的两次稳定快照逐字节一致（长度、容量、摘要、载荷全部相同）；
3. 长度、字段与摘要在同一次采样窗口内取得，窗口内任何版本变化都会作废该快照；
4. 载荷的 SHA-256 摘要与头部摘要匹配，且字段帧通过严格的结构化校验。

任一条件无法在重试预算内满足时返回 `ErrUncertain`（不确定，调用方丢弃本轮
结果即可）；稳定复现的结构损坏返回 `ErrMalformed`，稳定复现的摘要不符返回
`ErrDigestMismatch`。

## 序号回绕与写入代次

头部同时维护 `seq`（序列号）与 `gen`（写入代次）。`seq` 到达回绕点（生产为
uint64 自然回绕，测试可用小天花板）归零为偶数时，`gen` 递增。读取器把
`(gen, seq)` 作为整体版本比较，因此回绕前后的快照不会被误判为同一版本。

## 共享页布局

小端序，64 字节头部 + 记录区：

| 偏移 | 大小 | 字段 |
| --- | --- | --- |
| 0 | 4 | magic `SPSH` |
| 4 | 4 | version |
| 8 | 8 | gen 写入代次 |
| 16 | 8 | seq 序列号（偶稳定/奇写入中） |
| 24 | 4 | capacity 记录区容量 |
| 28 | 4 | length 当前记录长度 |
| 32 | 32 | 记录载荷的 SHA-256 |
| 64 | … | 变长记录区（TLV 字段帧） |

## 用法

内存页（测试/同进程）：

```go
page := netsec.NewMemPage(netsec.PageSize(4096))
writer, _ := netsec.NewWriter(page, 4096-64)
reader := netsec.NewReader(page, netsec.ReaderConfig{MaxAttempts: 32})

writer.Publish(netsec.Record{Fields: []netsec.Field{
    {ID: 1, Type: netsec.FieldUint64, Data: []byte{1, 0, 0, 0, 0, 0, 0, 0}},
}})
rec, err := reader.Read(ctx)
```

真实共享内存（Linux/macOS）：把低信任进程给出的 fd 用 `MapFile` 映射成
`Page` 后交给 `NewReader`；其他平台自行实现 `Page` 接口即可。

## 安全属性测试

- `TestNeverFabricatesRecord`：受控写入器在每轮读取之间改写长度、字段与
  摘要，并通过采样间钩子制造跨版本快照；所有被接受记录都按摘要在“真实
  存在过的记录集合”中逐字节核对，任何拼接记录都无法通过。
- `TestConcurrentWriterReader`：真实 goroutine 并发读写，race 检测下验证
  读取结果非真即不确定。
- `TestTornWindowIsRejected`、`TestMixedLengthAndDigestRejected`：脚本化
  撕裂窗口与“弗兰肯斯坦”页，验证重试/确定错误分类。
- `TestSequenceWraparoundWithGeneration`：小天花板下验证回绕与代次联动。
- `TestRetryBudgetExhausted`、`TestContextCancellation`：预算与取消语义。

运行：`go test -race -count=1 ./...`
