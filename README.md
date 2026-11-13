# P-256 Schnorr 随机化批量验证

## 背景：直接相加验证为何不安全

朴素批量验证把多份 Schnorr 签名的标量与点直接相加（等价于所有项系数
恒为 1）。攻击者可构造两份错误项让偏差互相抵消，例如：

- 签名 0 改为 `s0' = s0 + 1 mod q`，等式左式多出 `+G`
- 签名 1 改为 `s1' = s1 - 1 mod q`，等式左式多出 `-G`

两份签名单独验证都失败，但朴素求和时 `+G` 与 `-G` 抵消，整批被错误接受。
本包用每项独立的随机系数 `a_i` 破坏抵消条件：偏差变为 `a0·G` 与 `-a1·G`，
仅在 `a0 = a1` 时才抵消，概率约为 `1/q`（可忽略），且种子每批新取，
攻击者无法预先适配。

## 语义

- 曲线：NIST P-256，群阶记为 `q`。
- 规范点：65 字节非压缩 SEC1（`0x04` 前缀），在曲线上且非无穷远点。
- 规范标量：32 字节大端，取值 `[1, q-1]`。
- 挑战：`e = SHA256(固定标签 || P || R || 消息长度 || 消息) mod q`，
  消息长度为 8 字节大端前缀。
- 单验：`sG = R + eP`。
- 批量大小：1 至 256 项。
- 重复项：先拒绝重复的完整 `(P,R,s,消息)` 条目，返回全部重复索引。
- 系数：`a_i = SHA256(批次域标签 || 种子 || 整批转录 || BE32(i) || BE32(counter)) mod q`，
  每个索引独立计数，结果为 0 即递增计数器，保证 `a_i ∈ [1,q-1]`。
- 批量等式：`Σ a_i s_i G = Σ a_i R_i + Σ a_i e_i P_i`。

## 种子策略

- 种子固定为 32 字节，只能来自宿主：默认 `HostSeedProvider` 委托
  `crypto/rand`，每批调用一次、批批不同。
- 不接受请求方指定的种子，也不使用任何固定种子（确定性种子仅供测试注入）。
- provider 为 `nil` 或返回错误、长度异常时，整批自动退回逐份验证，
  结果方法标记为 `per-item-verify`。

## 失败定位

批量等式失败后，复用同一整批转录与种子派生的原索引系数递归二分：

- 子集等式通过只表示该子集在本批随机挑战下通过，不代表其中签名必然有效。
- 递归到叶子（单项）时一律执行单验，确保结论不依赖随机系数。
- 若出现“父集合失败而两个子集合均通过”的不一致，整批退回全部单验。
- 定位结果再经逐份交叉确认，返回精确无效索引；无法确认时同样整批单验。

## 审计输出

`Result` 记录：

- `Status`：`accepted` / `rejected` / `single-fallback` / `duplicates-rejected`
- `Method`：`batch-equation` / `recursive-bisection` / `per-item-verify`
- `Invalid`、`Duplicates`：精确索引（升序去重）
- `Coefficients`：实际使用的 `a_i`（32 字节大端）
- `SeedDigest`：宿主种子的 SHA-256 摘要（不暴露种子本身）
- `TranscriptDigest`：整批规范转录的 SHA-256 摘要

注意：批量成功是概率性接受。对安全敏感的调用方应把 `accepted` 理解为
“在本批新鲜随机挑战下等式成立”，并持久化种子摘要与转录摘要以便审计，
而不是把它等同于逐份确定性验证。

## 用法

```go
import "example.org/netsec/q0060"

// 密钥与签名（服务侧验签通常只收到公钥与签名）
priv, pub, err := netsec.GenerateKey(nil)
r, s, err := netsec.Sign(priv, message, nil)

// 单验
ok, err := netsec.VerifySingle(pub, r, s, message)

// 批验：传 nil 或让 HostSeedProvider 出错都会安全退回逐份验证
items := []netsec.Item{{Pub: pub, R: r, S: s, Msg: message}}
result, err := netsec.VerifyBatch(items, netsec.HostSeedProvider{})

switch result.Status {
case netsec.StatusAccepted:
    // 概率性接受，可记录 result.SeedDigest / result.TranscriptDigest
case netsec.StatusRejected:
    // result.Invalid 为精确无效索引；result.Method 标明二分或逐份路径
case netsec.StatusDuplicates:
    // result.Duplicates 为重复条目索引
case netsec.StatusFallback:
    // 种子缺失，已逐份验证
}
```

## 测试

`go test ./...` 覆盖：

- 真实 P-256 签名的签发/单验往返与消息篡改拒绝。
- 两项代数抵消攻击：复现朴素直接相加会接受两份坏签名，再证明随机系数
  批量在不同种子与宿主新鲜种子下均拒绝，无效集合 `[0 1]` 与逐份参考一致。
- 篡改消息、多坏项多层二分、单项批次叶子单验。
- 重复完整条目拒绝（含“仅消息不同不算重复”）。
- 种子缺失/出错退回逐份验证；不同种子产生不同系数与种子摘要、同一转录
  产生相同转录摘要。
- 系数 KDF 的规格向量、非零性、遇零递增计数器分支、规范点/标量拒绝、
  批次大小边界。

性能基线（Apple M1 Pro，256 项全有效批次）见 `BenchmarkVerify256`。
