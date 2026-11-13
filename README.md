# DNP3挑战认证被搬到另一控制操作

工业控制站对高风险操作发挑战，攻击者若能把一份认证响应用于另一功能码或对象，就能执行未批准控制。给Go网关实现题内挑战认证包：请求身份含masterAddress、outstationAddress、连接代次、应用序号、功能码、对象组/变体/索引、操作值和请求摘要；高风险请求先进入Pending并生成每连接内唯一的随机128位challengeID、32字节nonce、keyID和单调截止，不执行。主站响应含challengeID、keyID和HMAC-SHA256，MAC覆盖固定域标签及上述完整请求身份、nonce和挑战ID；keyID从固定主站密钥表取，不接受报文自带材料。验证成功后原子把对应Pending转Authorized并只执行原请求一次，超时、连接重置、应用序号重用、策略变更或另一个请求占用同一序号均取消。响应失败不执行且计入主站限速；同一有效响应重传返回原执行回执。多片应用请求先完整重组并冻结摘要后才能挑战，重组期间的字段不能单独授权。用两个功能码、两个对象和两个主站交错，测试响应搬运、字段改写、分片替换、挑战重放和回执丢失，操作回调记录必须与获MAC覆盖的身份逐字节一致。

## 实现概览

- `RequestIdentity` 是整个请求的冻结身份，`ComputeRequestDigest` 对其所有字段（不含摘要自身）计算 SHA-256。
- `Gateway.Submit` 只为高风险请求生成 Pending challenge：16 字节随机 `ChallengeID`、32 字节 `nonce`、固定密钥表中选择的 `KeyID` 和单调截止时间；此时不会调用操作回调。
- `Gateway.Respond` 用 `MACInput`（固定域标签 + 完整身份 + nonce + challengeID + keyID）重新计算 HMAC-SHA256。验证通过后在同一把锁内完成 Pending → Authorized，再在锁外执行回调一次。
- 同一 challenge 的有效响应重传从回执表返回原 `Receipt`，不会重复执行。超时、连接 reset、策略变更、序号重用和序号占用会取消 Pending。
- `Gateway.AddFragment` 按连接代次和应用序号重组分片；只有 final 分片到达并计算摘要后才产生 challenge，单个分片字段无法被单独授权。
- `MarshalIdentity` 和 `Execution.MACInput` 可用于审计：回调记录必须与 challenge 中受 MAC 保护的字节逐字节一致。

```go
gw, err := netsec.NewGateway(netsec.Config{
	Keys:         map[netsec.KeyID][]byte{"master-1": secret},
	KeyByMaster:  map[uint16]netsec.KeyID{0x0101: "master-1"},
	ChallengeTTL: 30 * time.Second,
	Executor: func(exec netsec.Execution) ([]byte, error) {
		// Execute only exec.Identity, the frozen challenged request.
		return nil, nil
	},
})

challenge, err := gw.Submit(identity) // enters Pending, does not execute
receipt, err := gw.Respond(netsec.Response{
	ChallengeID: challenge.ChallengeID,
	KeyID:       challenge.KeyID,
	MAC: netsec.ComputeResponseMAC(
		secret, challenge.Identity, challenge.Nonce,
		challenge.ChallengeID, challenge.KeyID,
	),
})
```
