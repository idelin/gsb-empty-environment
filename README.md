# 探针规则短路证据真实性

安全网关需要向审计方说明一次允许决定实际执行了哪些规则，不能事后按最终值伪造执行路径。写供Go规则宿主调用的短路执行包，表达式含AND、OR、NOT与可取消检查，每个检查返回判定和证据摘要；执行轨迹绑定节点、次序、返回值与规则版本，验证器独立重放并拒绝缺步或添加未执行分支。用异常、取消和共享节点测试证据不能证明未做过的检查。

## 用法

```go
allowCheck := netsec.CheckFunc(func(ctx context.Context) (netsec.CheckResult, error) {
	// 检查必须响应 ctx 取消。
	return netsec.CheckResult{
		Verdict:  netsec.VerdictAllow,
		Evidence: netsec.NewEvidence([]byte("tls=1.3;cert=ok"), "tls inspection"),
	}, nil
})
denyCheck := netsec.CheckFunc(func(ctx context.Context) (netsec.CheckResult, error) {
	return netsec.CheckResult{
		Verdict:  netsec.VerdictDeny,
		Evidence: netsec.NewEvidence([]byte("blocklist=hit"), "blocklist"),
	}, nil
})

policy := netsec.MustNewProgram("rules-2026-11-18",
	netsec.And(
		netsec.RuleRef("tls"),
		netsec.Not(netsec.RuleRef("blocklisted")),
	),
	[]netsec.Rule{
		{ID: "tls", Version: "v3", Check: allowCheck},
		{ID: "blocklisted", Version: "v7", Check: denyCheck},
	},
)

trace, err := policy.Evaluate(ctx)
if err == nil {
	err = policy.VerifyAllow(trace) // 独立重放路径，要求完整的 allow 结论
}
```

## 审计边界

- 每个 `Step` 绑定 `Node`（精确的表达式位置，而不是仅规则名）、`Seq`、`Verdict`、证据摘要和 `RuleVersion`。
- 共享表达式节点不记忆化；每到达一个位置就产生一次独立检查义务，因此第一个位置的证据不能证明第二个位置执行过。
- 异常、取消和检查错误都会结束轨迹，后续分支不会出现在轨迹中；`StepCanceled` 是无证据的停止标记，`Invoked()` 为 `false`。
- `Trace.Seal` 是字段哈希链，能发现事后改写，但不是签名。能整体重写轨迹的攻击者也能重算它。
- `Program.Verify` 独立重放表达式与步骤次序；`Program.VerifyLive` 还会用同样的 `Check` 重跑，并比较判定与证据摘要，用于发现按最终值伪造的一致轨迹。`VerifyLive` 只适用于确定性的、无不可逆副作用的检查。
- `Rule.Version` 必须随实现、输入或证据格式变化；程序摘要绑定策略版本、表达式和所有规则版本，但不绑定可执行代码。
