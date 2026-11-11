# 对象资源通配否定展开

云策略发布器要确认否定资源模式没有意外涵盖其他租户。实现供Go发布宿主调用的集合包，资源是有限段元组，每段域由输入给定，模式可列举允许及排除值；多个允许取并，显式拒绝优先。求最终可见集合与禁止租户集合的交集，输出最小见证，使用决策图并以小域枚举核对不展开巨大笛卡尔积。

## 用法

```go
spec := netsec.Spec{Segments: []netsec.Segment{
    {Name: "tenant", Domain: []string{"t1", "t2", "t9"}},
    {Name: "region", Domain: []string{"cn", "us"}},
}}
pol := netsec.Policy{
    Allow: []netsec.Pattern{{Segments: []netsec.SegmentFilter{
        {Allow: []string{netsec.Wildcard}, Deny: []string{"t1"}}, // 全部租户，排除 t1
        {Allow: []string{netsec.Wildcard}},
    }}},
    Deny: []netsec.Pattern{{Segments: []netsec.SegmentFilter{
        {Allow: []string{"t9"}}, // 显式拒绝优先
        {Allow: []string{"us"}},
    }}},
}
forbidden := []netsec.Pattern{{Segments: []netsec.SegmentFilter{
    {Allow: []string{"t9"}},
    {Allow: []string{netsec.Wildcard}},
}}}
res, err := netsec.Check(spec, pol, forbidden)
// res.Leaked == true, res.Count == 1, res.Witness == ["t9", "cn"]
```

## 设计

- 资源是有限段元组，每段域由 `Spec` 给定；`SegmentFilter` 的匹配集为 `Allow` 减 `Deny`，`*` 为通配符。
- 可见集合 = 允许模式之并 − 拒绝模式之并（显式拒绝优先）。
- 集合运算（并/交/差）在约简有序多值决策图（MDD）上进行，节点完备边 + 唯一表共享，复杂度正比于图大小而非笛卡尔积。
- 交集基数用 `big.Int` 精确计数（归约跳层按域大小前缀积折算）；最小见证按域序字典序在图上贪心取得。
- 当元组总数不超过 2^20 时，`Check` 自动用全枚举独立核对决策图的成员判定、计数与见证，任何分歧都报内部错误。
