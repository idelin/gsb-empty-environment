# fuzzdict

带权模糊词典检索库（Go）。

## 功能

- 静态词典构建在共享前缀的字典树上，重复词自动去重。
- 模糊查询支持四种编辑操作：插入、删除、替换、相邻交换，
  每种操作的代价由调用方给出的非负整数决定。
- 返回前 K 条**不同**词条，先按最小编辑代价升序，代价相同按词典序升序。
- 共享前缀剪枝：子树代价下界超过 `MaxCost` 或第 K 名代价时整枝跳过。
- 工作预算：`MaxExpansions` 限制展开的字典树节点数；预算耗尽时，
  每个返回结果的 `Proven` 字段标明该名次是否已被证明不再变化。

## 距离语义

距离定义为把词典中的词变换为查询串的最小总代价，用带权
Lowrance-Wagner 递推计算（完整的 Damerau-Levenshtein 距离，
允许同一子串被多次编辑，例如 `ca -> abc` 的代价是 `Transpose + Insert`）。

当 `2*Transpose >= Delete + Insert` 时，该距离与字符串编辑图
（顶点为字符串、边为单步编辑操作）上的最短路径完全一致；
否则它是编辑图距离的上界（精确计算在此区间是 NP 难的）。

## 用法

```go
dict := fuzzdict.New([]string{"abc", "abd", "bca", "cab"})
res, err := dict.Search(fuzzdict.Query{
    Pattern:       "abc",
    K:             3,
    Costs:         fuzzdict.Costs{Insert: 1, Delete: 1, Replace: 1, Transpose: 1},
    MaxCost:       -1, // 不限代价
    MaxExpansions: -1, // 不限预算
})
// res[0] = {Word:"abc", Cost:0, Proven:true}
```

`MaxCost < 0` 表示不限代价，`MaxExpansions < 0` 表示不限预算。

## 测试

```
go test ./...
```

测试用字符串编辑图上的 Dijkstra 最短路作为独立基准，
穷举短词（字母表 {a,b}、长度 ≤ 3 的全部词与查询串）复算，
覆盖零代价环（全零代价、免费插入/删除）、K 超出词数、
同词多条编辑路径，以及预算逐步增加时已证明名次的正确性。
