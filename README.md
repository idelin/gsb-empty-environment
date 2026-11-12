# 数据删除影响追踪

隐私删除请求不能只移除源记录，还需定位保留了其贡献的派生物。实现供Go数据安全宿主调用的溯源代数包，节点支持复制、过滤、聚合与按键连接，保存每个结果对源ID的精确依赖表达；删除后返回需重算对象和不受影响证明。共享子表达式压缩但不丢重复贡献，用小数据实际执行对照。

## 用法

```go
store := provenance.NewStore()
p := provenance.NewPipeline(store)

users := provenance.NewCopy(p, "users", userRecs)
adults := provenance.NewFilter(p, "adults", users, func(t provenance.Tuple) bool { return t.Val >= 18 })
orders := provenance.NewCopy(p, "orders", orderRecs)
joined := provenance.NewJoin(p, "user_orders", adults, orders)
provenance.NewAggregate(p, "order_counts", joined, provenance.AggCount)

imp := p.Impact("u1")          // 删除源记录 u1
for _, a := range imp.Recompute { /* 需重算对象，含溯源表达式与命中的删除源 */ }
for _, pr := range imp.Unaffected {
	if pr.Verify(imp.Deleted) { /* 不受影响证明：精确依赖集 + 摘要 */ }
}
```

- 溯源表达式为半环多项式（`Var`/`Sum`/`Prod`)，经哈希consing共享子表达式；`Sum`/`Prod` 保留重复参数，重复贡献不丢失（`Count` 按贡献路径计值）。
- 测试以删除后暴力重跑管线做对照：未受影响对象须逐字节一致，标记重算对象须消失或变化。
