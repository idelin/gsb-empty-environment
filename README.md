# PromQL租户选择器强制注入

多租户监控网关在每次查询前把租户标签强制写进选择器，攻击者用空选择器、对租户标签写正则放行，或把选择器藏进子查询和函数参数来读别人的指标。Go查询网关需要可导入的查询改写包，输入调用方给出的简化查询树（向量选择器带指标名与匹配器列表，范围选择器，函数调用，聚合带 by 或 without 标签列表，二元运算带 on、ignoring 与 group_left 标签列表，子查询，数字字面量）和租户标识：对树中每一个向量选择器（含子查询与函数参数内的）删去原有的 tenant 匹配器，再追加 tenant="租户" 精确匹配；label_replace 或 label_join 的目标标签为 tenant、任一 by、without、on、ignoring、group_left 列表含 tenant 时，整条查询拒绝；没有向量选择器的查询原样放行；指标名缺省且匹配器都能匹配空串的选择器同样要追加。用 {tenant=~".+"}、子查询内选择器、label_replace 改写 tenant、空匹配器和嵌套函数验证每个选择器都被约束。

## 包用法

包名为 `netsec`，入口是 `netsec.Rewrite(query netsec.Node, tenant string) (netsec.Node, error)`，
在调用方解析出的简化查询树上就地改写：

- 遍历整棵树（含函数参数、二元运算两侧、子查询内部），对每个 `*VectorSelector`
  删除原有 `tenant` 匹配器并追加 `tenant="<租户>"` 精确匹配；
- `label_replace`/`label_join` 目标标签为 `tenant`，或 `by`、`without`、`on`、
  `ignoring`、`group_left` 列表含 `tenant` 时，返回包装了
  `netsec.ErrTenantLabelForbidden` 的错误，调用方应拒绝整条查询；
- 没有向量选择器的查询（如纯数字字面量运算）原样返回；
- 空租户标识返回 `netsec.ErrEmptyTenant`。

```go
import "example.org/netsec/q0729"

rewritten, err := netsec.Rewrite(queryTree, tenantID)
if errors.Is(err, netsec.ErrTenantLabelForbidden) {
    // 拒绝查询
}
```

`Matcher.MatchesEmpty` 与 `VectorSelector.MatchesEmpty` 用于识别指标名缺省、
且所有匹配器都能匹配空串的选择器；这类选择器同样会被强制追加 tenant 约束。
