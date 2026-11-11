# 附加权限最小差集

审查服务要指出云策略更新新开放了哪些操作，不能只比较文本增删。写供Go审查宿主调用的差分包，主体、动作、资源和标签取有限域，策略为布尔条件加允许拒绝，拒绝优先；求新允许且旧拒绝的互不重叠矩形覆盖并附命中规则。允许分片预算，超限返回尚未覆盖的余域，缩小域穷举验证无漏无重。

## 模型

有限域宇宙 `Universe` 由主体（principal）、动作（action）、资源（resource）
三个必选轴和零到多个标签轴（tag）构成，每个轴取值有限。一个具体操作是各轴
取值组成的元组 `Point`。

策略 `Policy` 是有序规则列表，规则 `Rule` 含效果（`Allow`/`Deny`）和布尔条件
`Condition`（`In` 成员判断、`And`、`Or`、`Not`）。求值时扫描所有命中规则：
任意 `Deny` 压倒 `Allow`（拒绝优先），无规则命中时默认拒绝。

差分目标是语义集合，而非文本增删：

```
target = { p : oldPolicy 对 p 非允许 且 newPolicy 对 p 显式允许 }
```

因此规则改写、合并、换顺序只要决策不变，就不会产生任何发现。

## 算法

1. 把每条规则的条件编译成互不相交的真值矩形集（`condition.go`）。
2. 以旧/新策略全部规则边界反复切分全域，得到决策与命中规则恒定的
   互不相交单元（`diff.go` 中的 `splitCells`）。
3. 对每个单元取代表点求值，保留“旧拒绝、新允许”的单元。
4. 命中注解相同且轴对齐的相邻单元合并成尽可能大的矩形
   （`rect.go` 中的 `mergeRects`），输出矩形两两不重叠。
5. 每个 `Finding` 携带矩形、旧/新策略各自命中的规则列表和元组计数。

## 分片预算

`Diff` 返回分页器 `Pager`，`budget` 为每页最多返回的矩形数；超限时当前页带
`Remainder`（尚未覆盖的余域，本身也是互不相交矩形覆盖），最后一页
`Complete` 为 true 且余域为 nil。宿主可持久化余域后继续翻页。

## 缩小域穷举验证

`Universe.Shrunk` 在原有限域内裁出更小的子宇宙；`Verify` 逐元组穷举两个策略的
真实决策，校验：

- 无漏：每个目标元组都被发现矩形或余域覆盖；
- 无重：发现矩形之间没有任何公共元组（`OverlapPoints == 0`）；
- 无溢：被覆盖的元组恰好都是目标元组；
- 命中规则：矩形附带的 `OldHits`/`NewHits` 与实际点火规则完全一致。

## 最小示例

```go
u, _ := netsec.NewUniverse(
    []string{"alice", "bob"},
    []string{"read", "write"},
    []string{"bucket"},
    netsec.TagDomain{Key: "env", Values: []string{"dev", "prod"}},
)
oldP := netsec.Policy{Rules: []netsec.Rule{
    {ID: "read", Effect: netsec.Allow,
        Condition: netsec.In{Axis: netsec.ActionAxis, Values: []string{"read"}}},
}}
newP := netsec.Policy{Rules: []netsec.Rule{
    {ID: "read", Effect: netsec.Allow,
        Condition: netsec.In{Axis: netsec.ActionAxis, Values: []string{"read"}}},
    {ID: "write", Effect: netsec.Allow,
        Condition: netsec.In{Axis: netsec.ActionAxis, Values: []string{"write"}}},
}}
pager, _ := netsec.Diff(u, oldP, newP, 100)
for {
    report := pager.Next()
    for _, f := range report.Findings {
        // f.Rect 即新开放操作的矩形，f.NewHits/f.OldHits 为命中规则
        _ = f
    }
    if report.Complete {
        break
    }
    // report.Remainder 为尚未覆盖的余域，可持久化后继续 Next()
}
```
