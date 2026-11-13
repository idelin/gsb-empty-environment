# PSBT 签名前策略包

为 Go 签名器提供 BIP174 v0 PSBT 的签名前策略检查。策略包不持有私钥、不访问网络；密钥派生与链上 prevout 查询都由调用方注入。

## 安全规则

- 只把 `scriptPubKey` 与“本机密钥按 PSBT 声明 BIP32 路径派生出的脚本”逐字节相等的输入放进 `SignInputs`；其余输入标记为跳过。
- 非隔离见证输入必须带 `non_witness_utxo`，且其交易哈希得到的 txid 必须等于输入 `prevout`，输出索引和 scriptPubKey 也必须一致。
- 隔离见证输入的金额和 scriptPubKey 以调用方的 `ChainSource` 为准；PSBT 的 `witness_utxo` 若存在则必须与链上来源完全一致。
- 所有输入金额都必须来自可信证据，手续费 `sum(inputs) - sum(outputs)` 不得超过配置上限。
- 只有输出脚本等于本机密钥按声明路径派生结果时才算找零；其他输出全部进入 `DisplayOutputs`。
- `sighash_type` 缺省视为 `SIGHASH_ALL`；任何不是 `SIGHASH_ALL` 的显式值都会拒绝整个 PSBT。
- 无法确认金额或有歧义的输入不会被“跳过放行”，而是拒绝整个 PSBT，避免攻击者用虚假金额掩盖真实手续费。

## 用法

```go
psbt, err := netsec.ParsePSBT(rawPSBT)
if err != nil {
	return err
}

policy := netsec.NewPolicy(
	// 只对本机指纹返回派生脚本；其他指纹返回 netsec.ErrNotMine。
	func(fingerprint [4]byte, path []uint32) ([]byte, error) {
		return deriveScriptPubKey(fingerprint, path)
	},
	// txid 使用区块浏览器/RPC 的显示字节序，与 OutPoint.TxID 一致。
	func(txid [32]byte, vout uint32) (int64, []byte, error) {
		return chainLookup(txid, vout)
	},
	5_000, // 最大手续费，单位 satoshi；0 表示只接受零手续费
)

decision, err := policy.Evaluate(psbt)
if err != nil {
	// 包含 *netsec.PolicyError；用 errors.Is 判断具体哨兵错误。
	return err
}

for _, index := range decision.SignInputs {
	signInput(psbt, index, decision.Inputs[index].Path)
}
for _, index := range decision.DisplayOutputs {
	showOutputToUser(psbt, index)
}
```

`ChainSource` 对未知 prevout 返回 `netsec.ErrPrevoutNotFound`。legacy 输入可退回 `non_witness_utxo` 验证；隔离见证输入则必须有链上金额证据。

## 策略结果

- `Evaluation.SignInputs`：允许签名的输入索引。
- `Evaluation.DisplayOutputs`：必须展示给用户的外部输出索引。
- `InputDecision.Sign/Owned/Segwit`：输入归属及见证类型。
- `OutputDecision.Change/Show`：输出是否已证明为找零。
- `PolicyError.Code`：稳定错误码，例如 `amount_mismatch`、`sighash_not_all`、`fee_limit_exceeded`。

## 测试

```sh
go test -race ./...
```

回归测试覆盖虚报隔离见证金额、伪造找零路径、外来输入/路径冒用、非 `SIGHASH_ALL`、超限手续费，以及 legacy `non_witness_utxo`、txid 不匹配和嵌套 P2SH 隔离见证的正反例。
