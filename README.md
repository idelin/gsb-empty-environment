# TPM策略OR树求值

TPM 对象的授权由策略摘要决定，攻击者可构造只满足 PolicyOR 某一分支、或混入别的对象摘要的会话来绕过 PCR 条件。该包为 Go 设备配置服务提供下发前调用的策略求值入口：

- AND 步骤按顺序执行，`policyDigest = H(旧摘要 ‖ 命令码 ‖ 参数)`。
- PolicyOR 先检查当前摘要是否属于可信分支列表，再将摘要清零并计算 `H(0 ‖ 命令码 ‖ 分支摘要...)`。
- 分支列表最多 8 项，且始终来自可信对象策略，绝不从请求或会话轨迹拷贝。
- 命令码由调用方通过 `CommandCodeTable` 注入。
- 仅当最终摘要与 TPM 对象的 `authPolicy` 一致时返回 `nil`。

```go
commands := netsec.DefaultCommandCodeTable()

policy := netsec.NewPolicy(
    netsec.Extend(netsec.KeyPolicyPCR, pcrDigest),
    netsec.OR(pcrBranchA, pcrBranchB),
)

if err := netsec.Evaluate(policy, commands, objectAuthPolicy); err != nil {
    // 拒绝下发
}
```

`pcrBranchA`、`pcrBranchB` 和 `objectAuthPolicy` 必须取自可信的设备/对象元数据。嵌套 OR 通过连续两个 OR 步骤表达：内层 OR 的输出摘要作为外层 OR 的可信分支之一。AND 步骤顺序、OR 分支顺序和完整分支列表都会进入摘要，任何调换或替换都会导致最终 `authPolicy` 不匹配。

测试覆盖单分支遍历、单分支列表、伪造分支列表、混入其他对象摘要、9 个分支、嵌套 OR、AND 顺序调换和 OR 分支顺序调换。
