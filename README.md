# 附加权限最小差集

审查服务要指出云策略更新新开放了哪些操作，不能只比较文本增删。写供Go审查宿主调用的差分包，主体、动作、资源和标签取有限域，策略为布尔条件加允许拒绝，拒绝优先；求新允许且旧拒绝的互不重叠矩形覆盖并附命中规则。允许分片预算，超限返回尚未覆盖的余域，缩小域穷举验证无漏无重。

## 用法

- `Domain` / `Rect`：有限域与矩形（逐维取值集合，缺省维为通配；标签即 `tag:*` 维）。
- `Policy` / `Rule`：布尔条件加 `Allow`/`Deny`，`Policy.Allows` 拒绝优先、默认拒绝。
- `Diff(newPol, oldPol, dom, budget)`：返回“新允许且旧拒绝”的互不重叠矩形覆盖，每个矩形附 `GrantedBy`（新策略命中允许规则）与 `PreviouslyDeniedBy`（旧策略命中拒绝规则，空表示默认拒绝）。`budget` 为分片预算（0 不限）；超限时 `Complete=false`，`Remainder` 为尚未覆盖的余域，且 `Covered ⊆ diff ⊆ Covered ∪ Remainder`、全部矩形两两不交。
- `Clip(pol, rect)`：把策略裁剪到余域矩形上，供分片重跑 `Diff` 后合并。
- `Verify(res, newPol, oldPol, dom)`：缩小域上穷举验证无漏、无重、无误覆盖。

```sh
go test ./...
```
