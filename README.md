# 精确投影合成组件

模块根包 `projection` 是可直接导入的 Go 协议注册组件。它接收有限状态、精确有理数权重和可见标签描述的协议，求保留类别数最多的安全公开投影，并输出可独立复算的安全证书与最优性证书。`SecretID` 只表示协议中的竞争性秘密初态假设，不包含、也不会生成实际用户标识。

## 语义

- `Protocol.Initial` 给出多个秘密初态假设；准入要求它们在同一公开历史下不可区分，直到宿主批准发布类别。
- `Transition.Probability` 支持 `"1/3"`、`"1"`、`"0.125"` 等精确写法；也可直接设置 `Transition.Weight *big.Rat`。离开一个状态的全部权重之和不得超过 1，剩余质量就是可观察的提前终止概率。
- 普通事件只输出投影后的 `Label`。宿主接受签名的 `Release` 事件额外公开 `Category`；未签名或被 `Protocol.VerifyRelease` 拒绝的类别不会公开，该边按普通事件处理。
- 对相同发布类别，后续投影标签、后续状态和提前终止必须具有相同的条件分布。不同发布类别允许分开两个秘密历史。
- 安全性使用完整公开轨迹上的精确概率双模拟和有理数运输方案，不检查单轮边缘分布，也不采样。普通标签的下一层分布必须与其全部后续分布一致，因此两轮相关性不同也会被拒绝。

## 合并约束

- `AllowedMerges` 是提示，不是可信闭包。组件会取对称传递闭包：例如只给 `A-B`、`B-C` 也会得到 `A-C` 可合并。
- 一个最终类别块必须是允许闭包中的团，并且不能包含任何 `ForbiddenMerges` 对。两个分别合法的合并如果合在一起会跨越业务禁区，该组合块不会被接受。
- 搜索枚举所有合法分区，优先类别数最大；并列时按排序标签上的 `label=representative` 规范编码字典序决胜。
- 原始标签总数上限为 12。

## 使用

```go
protocol := projection.Protocol{
    Initial: map[projection.SecretID]projection.StateID{
        "low":  "s-low",
        "high": "s-high",
    },
    Transitions: []projection.Transition{
        {From: "s-low", To: "joined", Label: "A", Probability: "1"},
        {From: "s-high", To: "joined", Label: "B", Probability: "1"},
        {From: "joined", To: "joined", Label: "R", Probability: "1",
            Release: &projection.Release{Category: "tier", Signed: true}},
    },
}

config := projection.Config{
    Labels:        []projection.Label{"A", "B", "R"},
    AllowedMerges: []projection.MergePair{projection.Pair("A", "B")},
}

result, err := projection.Synthesize(protocol, config)
if err != nil {
    // ErrNoSafeProjection 表示所有合法投影都不安全。
}
err = projection.VerifyCertificate(protocol, config, result.Certificate)
```

主要入口：

- `Synthesize`：返回最优安全投影、类别数和两部分证书。
- `CheckSafety` / `IsSafe`：对指定投影给出安全性证明或反例。
- `VerifySafetyCertificate`：只检查双模拟关系、终止概率、普通概率和发布后条件分布的运输方案。
- `VerifyCertificate`：复算最优投影、检查全部提交反例并重放穷举摘要。

安全证书包含候选双模拟关系及每条边的有理数耦合；最优性证书包含所有保留类别数更多的合法投影的数量和顺序无关 SHA-256 摘要，并附最多 `MaxRefutationWitnesses` 个具体反例。`VerifyCertificate` 会重新枚举，因此修改证书中的投影、类别数或摘要都会失败。

## 回归表

`projection_test.go` 用小表覆盖：

- 先出现普通标签泄漏、后出现签名发布时，发布点不能回溯豁免早期泄漏。
- 两个秘密假设的单轮边缘分布相同，但两轮联合相关性不同。
- 提前终止概率不同即不通过。
- 非传递的允许合并提示按传递闭包处理。
- 两个分别允许的合并组合后跨越 `ForbiddenMerges` 时，不回退成不安全合并。
- 未签名发布类别不得让秘密初态提前分开。

运行：

```sh
go test ./...
go vet ./...
```
