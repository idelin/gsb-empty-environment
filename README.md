# NetworkPolicy命名空间选择交并

多租户集群用 NetworkPolicy 隔离命名空间，攻击者给自己的命名空间打上 team=payments 这类可自改标签，或利用 from 里 namespaceSelector 与 podSelector 写成同一项还是两项的差别。为Go控制器实现可导入的入站放行判定库：同一 from 元素内的 namespaceSelector 与 podSelector 取交集，不同元素取并集，空选择器 {} 选中全部，仅有 podSelector 时限定在策略所在命名空间；输入里 kubernetes.io/metadata.name 之外的命名空间标签视为租户可改，凡放行依赖可改标签时都在结果中标出。用交集与并集写法、空选择器和可改标签命名空间验证。

## 安装与导入

模块路径为 `example.org/netsec/q0141`，包名为 `netsec`，只依赖 Go 标准库：

```go
import netsec "example.org/netsec/q0141"
```

## 判定模型

`EvaluateIngress(policy, source, destination)` 先检查目的 Pod 是否位于策略命名空间并由策略级 `PodSelector` 选中，再按以下规则计算入站来源：

- `IngressRule.From` 为空：该规则允许所有来源，与 Kubernetes 语义一致。
- 不同 `from` 元素、不同 ingress 规则之间取并集。
- 同一 `from` 元素内，`NamespaceSelector` 与 `PodSelector` 同时存在时取交集。
- 只有 `PodSelector` 时，来源命名空间必须是策略所在命名空间。
- 只有 `NamespaceSelector` 时，选中命名空间内的所有 Pod 都允许。
- 两个选择器都不存在（空 peer）以及空 `LabelSelector{}` 均选中全部对应范围。
- `LabelSelector` 同时支持 `matchLabels` 与 `matchExpressions`（`In`、`NotIn`、`Exists`、`DoesNotExist`）。

命名空间的 `Name` 是权威身份；`kubernetes.io/metadata.name` 由它派生，调用方不能通过标签伪造。除此之外的命名空间标签都视为租户可改。若某个允许路径使用了这类标签，结果会设置
`ReliesOnMutableNamespaceLabels`，并在 `MutableNamespaceLabels` 中列出命名空间、标签键、操作符与值。拒绝结果不会报告放行依赖。

## 使用示例

```go
policy := netsec.NetworkPolicy{
    Namespace:   "payments",
    PodSelector: netsec.LabelSelector{}, // 选中 payments 内所有 Pod
    Ingress: []netsec.IngressRule{{
        From: []netsec.PeerSelector{
            {
                // 同一元素内取交集：team=payments 的命名空间中，
                // 只有 app=api 的 Pod 被允许。
                NamespaceSelector: &netsec.LabelSelector{
                    MatchLabels: map[string]string{"team": "payments"},
                },
                PodSelector: &netsec.LabelSelector{
                    MatchLabels: map[string]string{"app": "api"},
                },
            },
            {
                // 另一个元素取并集：shipping 命名空间中的所有 Pod。
                NamespaceSelector: &netsec.LabelSelector{
                    MatchLabels: map[string]string{
                        netsec.MetadataNameLabel: "shipping",
                    },
                },
            },
        },
    }},
}

decision := netsec.EvaluateIngress(
    policy,
    netsec.Pod{
        Namespace: netsec.Namespace{
            Name:   "attacker",
            Labels: map[string]string{"team": "payments"},
        },
        Labels: map[string]string{"app": "api"},
    },
    netsec.Pod{
        Namespace: netsec.Namespace{Name: "payments"},
        Labels:    map[string]string{"app": "web"},
    },
)

// decision.Allowed == true
// decision.ReliesOnMutableNamespaceLabels == true
// decision.MutableNamespaceLabels[0].Key == "team"
```

该示例展示了跨租户风险：攻击者可以给自己的命名空间打上 `team=payments`，因此控制器应告警或拒绝依赖该标签的放行路径。使用 `kubernetes.io/metadata.name`、命名空间身份比较，或同一元素内的 Pod 选择器可以避免依赖可改标签。

## 范围

本库针对选择器语义的入站来源判定，不处理端口、`IPBlock`、egress 或 `policyTypes`。这些字段应结合控制器所需的完整策略模型另行处理。

## 验证

```sh
go test ./...
```

测试覆盖同元素交集与跨元素并集的区别、空选择器、仅 `podSelector` 的命名空间限制、`kubernetes.io/metadata.name` 不可伪造，以及放行时对 `team` 等可改命名空间标签的标记。
