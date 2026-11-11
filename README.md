# 证书路径策略集合

企业接入层需要按证书策略限制客户端用途，找到任意可信链并不代表策略也成立。写供Go授权层调用的策略求值包，输入已经验签的候选链和每层允许策略集合、映射及禁止映射标记；从叶子用途反向求可接受根策略，空集必须拒绝，映射只能在声明的相邻层发生。返回完整映射路径，用穷举有限策略集合核对交叉链、环形映射和两条链不能拼接取长补短。

## `policyeval` 子包

候选链假定已完成验签，本包只回答“策略是否成立”。层按信任锚（索引 0）到叶子排列：

- `Layer.AllowedPolicies`：该层 `certificatePolicies`，至少一个 OID；`AnyPolicy`（`2.5.29.32.0`）作为通配。
- `Layer.Mappings`：本层（subject 侧）声明的 `IssuerDomain -> SubjectDomain` 映射，只作用于本层与上层之间的这一条相邻边界；信任锚层不得带映射。
- `Layer.InhibitMapping`：置位后该边界禁止映射，只允许 OID 原样（恒等）穿越。

求值从叶子用途反向枚举每条完整映射路径，直到信任锚；任一中间或根层可接受集合为空即拒绝，绝不默认放行。

```go
ok, accepted, err := policyeval.Authorize(candidateChains, leafUsage)
if err != nil { /* 输入结构错误 */ }
if !ok { /* 没有任何候选链在策略上成立 */ }

for _, result := range accepted {
    for _, path := range result.Paths {
        _ = path.RootPolicy // 可接受的根策略
        for _, step := range path.Steps {
            _ = step.AppliedMapping // nil 表示恒等，否则记录该步应用的映射
        }
    }
}

// 穷举有限策略宇宙，核对反向求值与前向枚举一致，并检测环形映射、跨链拼接：
report, err := policyeval.ExhaustiveAudit(candidateChains, leafUsage)
_ = report.OK             // 每条链枚举与求值一致，且不存在“取长补短”的拼接
_ = report.SplicePossible // 联合图中是否出现单链都无法独立产出的混合走法
```

候选链之间严格隔离：每条链只接收自己的层，策略不能跨链借用；`ExhaustiveAudit` 另外在所有链的联合图上枚举混合走法，显式证明两条各自失败的链无法拼接出授权。
