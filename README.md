# 密码协议转录编码

认证协议把可变长字段直接连接后签名，存在不同消息共用转录的问题。给Go协议宿主实现可导入的转录构造包，字段带唯一标签、长度和消息阶段，整数使用唯一最短编码；未知可选字段保留参与摘要，重复标签与阶段回退拒绝。支持分块大字段及中途取消，关闭前不能导出可签摘要。用构造碰撞对、所有切块位置和阶段重试验证不同合法结构不会得到相同编码。

## 实现

可导入包位于 `transcript`（导入路径 `example.org/netsec/uid1271/transcript`）。

### 规范帧结构

每个字段编码为自描述帧，所有头部整数均为最短 LEB128 varint：

```
phase || tag || type || len(payload) || payload
```

- 显式 `len` 界定边界，裸拼接的字段越界、空字段歧义在结构上不可能发生。
- `tag` 在整条转录中唯一；`0` 预留给关闭帧，应用字段标签必须大于 0。
- `type` 区分字节串、无符号整数、有符号整数，避免类型混淆。
- 无符号整数用最短无符号 LEB128；有符号整数用最短 ZigZag LEB128，零只有一种编码，非最短编码一律拒绝。
- 关闭时追加域分离结束帧（保留标签 0，载荷为字段计数），防止结束标记与数据帧混淆。

### 状态与安全规则

- 阶段只能前进，回退返回 `ErrPhaseRollback` 且不改变状态，可直接重试。
- 重复标签（含跨阶段、原始帧重放）返回 `ErrDuplicateTag`，标签 0 返回 `ErrBadTag`。
- 未知可选字段通过 `WriteRawFrame` 以已编码帧注入：完整校验后原样参与摘要，无需宿主理解其 schema；畸形帧、非最短 varint、尾随字节、阶段不匹配均拒绝。
- 大字段用 `BeginField` 声明长度后分块写入（`io.Writer`），超长块整体拒收且不消费字节，未写满不能关闭字段。
- `Cancel` 可在任意时刻（含字段写到一半）中止，重置哈希状态，之后所有操作返回 `ErrCancelled`，永远无法导出摘要。
- `Close` 成功前 `Digest` 返回 `ErrNotClosed`，保证不会对半成品转录签名。
- 构造器内部加锁，可被多 goroutine 安全使用；同一时刻只允许一个打开的字段。

### 主要 API

```go
b := transcript.New()
b.BeginPhase(1)                 // 推进消息阶段
b.WriteUint(tag, value)         // 最短无符号整数
b.WriteInt(tag, value)          // 最短 ZigZag 有符号整数
b.WriteBytes(tag, payload)      // 整段字节字段
fw, _ := b.BeginField(tag, n)   // 分块大字段
fw.Write(chunk); fw.Close()
b.WriteRawFrame(frame)          // 未知可选字段，校验后保留参与摘要
d, err := b.Close()             // 关闭后才能拿到可签摘要
d.Sum                           // [32]byte SHA-256
b.Cancel()                      // 中途取消
```

### 验证

`go test ./...`（另可用 `-race` 运行）覆盖：

- `TestCollisionPairs`：手工构造边界漂移、空字段、类型混淆、阶段差异、一字段对两字段等碰撞家族，断言摘要两两不同。
- `TestAllChunkPositions` / `TestChunkFailuresAtEveryPosition`：对每个切块位置（含零长度写）验证分块与整段编码逐字节同摘要，并验证每个位置的超长/提前关闭拒收且状态不变。
- `TestPhaseRetry`：阶段回退与重复标签拒收后，合法调用可重试，最终摘要与无错误路径完全一致。
- `TestUnknownRawFrame`、`TestIntegerCanonicalEncoding`、`TestEndFrameDomainSeparation`、`TestRandomizedNoCollision`、`TestCancellation`、`TestDigestGating`、`TestConcurrentFields` 覆盖原始帧、整数规范性、域分离、随机无碰撞、取消、关闭门禁与并发。
