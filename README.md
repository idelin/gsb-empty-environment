# 供应链见证人独立性

制品发布要求多个独立机构作见证，持有同一控制方的多个证书不能重复计票。
`netsec` 是供 Go 发布宿主调用的资格求解包：输入见证签名、机构归属和控制关系图，
输出满足阈值的最小合格见证集（最多 20 个）。

## 求解流程

1. **控制域合并**：用并查集（DSU）按控制关系图的可达性合并机构。控制边按无向连通
   处理，控制链、环、菱形层级中的机构全部归入同一控制域，域 ID 取域内最小机构 ID。
2. **证书别名归一**：签名经证书追溯到持有机构与控制域；同一等价类
   （控制域/机构/地区/类别/证书）的多个签名只保留一个确定性原子，每个控制域至多投一票。
3. **子集枚举**：在域选项上做迭代加深 DFS（先取后跳，保证同尺寸下取字典序最小解），
   配合后缀地区/类别/组合剪枝，找到满足总见证数、地区数、机构类别数和
   `地区|类别` 组合阈值的最小集合；尺寸上限为 20，枚举节点预算 `enumBudget` 防止失控。

## 类型与入口

- 入口：`func Solve(in Input) (*Result, error)`
- 输入：`Input{Organizations, Certificates, ControlEdges, Signatures, Thresholds, MaxWitnesses}`
- 输出：`Result{Witnesses, ControlDomain, EnumVisited}`
- 错误：`ErrEmptyInput`、`ErrUnknownReference`、`ErrDuplicateSignature`、`ErrNoQualifiedSet`

`Thresholds` 各字段为零时表示不约束；`MinPairs` 的键格式为 `"region|category"`。
`MaxWitnesses <= 0` 时默认取协议上限 20，大于 20 直接报错。

## 宿主调用示例

```go
res, err := netsec.Solve(netsec.Input{
    Organizations: orgs,
    Certificates:  certs,
    ControlEdges:  edges,
    Signatures:    sigs,
    Thresholds: netsec.Thresholds{
        MinWitnesses:  3,
        MinRegions:    3,
        MinCategories: 2,
        MinPairs:      map[string]int{"eu|ca": 1},
    },
})
if errors.Is(err, netsec.ErrNoQualifiedSet) {
    // 没有任何满足阈值的独立见证组合
}
for _, w := range res.Witnesses {
    // w.SignatureID / w.ControlDomain / w.Region / w.Category
}
```

## 验证

- `go test ./...`：覆盖控制链/菱形/环合并、交叉控制计票、证书别名去重、
  地区与类别组合阈值、最小性、确定性、20 上限与非法输入。
- `go vet ./...`、`gofmt -l .` 保持干净；结果在重复调用间确定且按 `SignatureID` 排序。
