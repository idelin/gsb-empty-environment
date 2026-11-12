# 日志收集器收到合法签名序号后，攻击者可能用另一设备的记录补齐缺口

日志收集器收到合法签名序号后，攻击者可能用另一设备的记录补齐缺口。为Go审计宿主实现前缀证明核验包，设备公钥、启动挑战、序号和前项摘要共同签名，封条签名绑定最终序号与根摘要；只有同设备同启动的完整链才能证明某前缀齐备。支持乱序、分叉与跨分片合并，分叉返回两份有效签名而不是任取一条。用标准库Ed25519构造删项、替换和跨设备拼链，并核对不完整链不能获得完整封存证明。

## 设计

- `Record` 的 Ed25519 签名覆盖 `Device || Boot || Seq || Prev || Payload`；`Record.Link()` 是这些字段的 SHA-256，后续记录以它为 `Prev`。
- `Seal` 的 Ed25519 签名覆盖 `Device || Boot || Seq || Root`，绑定最终序号与链根摘要。
- 记录只能在其 `Device + Boot` 上下文内合并。`VerifyPrefix` 遇到混合上下文直接拒绝，不把另一设备的记录补进缺口。
- `Analyze` 按上下文分别验证，收集每一个序号上的所有有效分支。`Fork.Records` 与 `Fork.Signatures()` 同时保留两份（或多份）签名，绝不默认选择一条。
- `VerifyPrefix` / `ProvePrefix` 只有在请求范围内从创世记录开始连续、链接一致且封条匹配时才返回 `Sealed == true`。删除、替换、跨设备拼接、提前截断都会得到 `ErrIncomplete`、`ErrFork` 或 `ErrMixedContext`。

## API 示例

```go
rec, _ := taskpkg.NewRecorderFromSeed(seed, boot)
r0, _ := rec.Append([]byte("first"))
r1, _ := rec.Append([]byte("second"))
seal, _ := rec.Seal()

proof, err := taskpkg.VerifyPrefix([]taskpkg.Record{r1, r0}, &seal)
// proof.Records 已按序号排序，proof.Sealed == true
```

`MergeShards` 合并重叠分片并去除完全重复记录；`Analyze` 的 `Report.Forks` 保留分叉的完整有效签名；`ProvePrefix(records, end, seal)` 用于显式核验某个较短前缀。
