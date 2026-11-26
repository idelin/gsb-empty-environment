# joinenum — conflict-aware bushy join enumeration

`joinenum` 是一个可导入的 Go 包，用于在同时包含 `InnerJoin`、
`LeftOuterJoin`、`SemiJoin`、`AntiJoin` 的查询中做精确 Bushy 连接枚举。
它按 Moerkotte & Neumann 的 core-search-space 模型实现：

- **TES（Total Eligibility Set）**：每个算子的最小需求基表集，即谓词实际
  引用的基表。左外/半/反半连接另有侧约束：谓词右侧引用必须由受保护的
  右输入提供，谓词左侧引用不得进入右输入。
- **Conflict Rules**：对原树中每一对祖先/后代算子，检查交换律与
  L-ASSCOM / R-ASSCOM 是否保持语义；不保持时生成 `T1 -> T2` 规则，
  禁止把 `T2` 算子移入 `T1` 算子的左侧（保留侧）或右侧（补空/存在性侧）。
- **连通子集 DP**：按 DPhyp 风格的连通子集拆分 `(S1, S2)` 枚举 Bushy 树，
  每个拆分都检查算子 TES、左右侧 schema 条件及全部冲突规则。
- **精确整型代价**：选择率是有理数，基数和代价均用 `int64` 精确计算；
  并列时按规范前序编码的字典序选择最小者。

## 快速使用

```go
package main

import (
	"fmt"
	"prescreen.local/ps-d33aa572a9" // package name: joinenum
)

func main() {
	rels := []joinenum.Relation{
		{Name: "A", Cardinality: 5, Attributes: []string{"k"}},
		{Name: "B", Cardinality: 4, Attributes: []string{"k"}},
		{Name: "C", Cardinality: 3, Attributes: []string{"k"}},
	}
	pAB := joinenum.Eq("p", joinenum.Sel(1, 2),
		joinenum.Column{Rel: 0, Attr: "k"},
		joinenum.Column{Rel: 1, Attr: "k"})
	pAC := joinenum.Eq("q", joinenum.Sel(1, 2),
		joinenum.Column{Rel: 0, Attr: "k"},
		joinenum.Column{Rel: 2, Attr: "k"})

	// (A LEFT OUTER B) INNER C
	root := joinenum.Inner(
		joinenum.LeftOuter(joinenum.Rel(0), joinenum.Rel(1), pAB),
		joinenum.Rel(2),
		pAC,
	)

	q, err := joinenum.NewQuery(rels, root)
	if err != nil {
		panic(err)
	}
	res, err := q.Optimize()
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Encoding, res.Cost, res.Cardinality)
	// L(I(A,C),B) 47 20
}
```

顶层入口也可以直接使用 `joinenum.Optimize(joinenum.Input{...})`。

## 输入模型

`Node` 是原始语法连接树：叶子用 `Rel(i)`，内部节点用
`Inner/LeftOuter/Semi/Anti(left, right, pred)`。每个内部节点携带谓词：

- `Refs`：谓词引用的基表，供 TES 与冲突规则使用；
- `Selectivity`：精确有理数选择率；
- `NullPreserving`：谓词是否可能在补空输入上为真（如 `IS NULL`）；
- `Match`：可选的真实数据求值函数。

`NewQuery` 会校验树形、关系是否只出现一次、谓词是否跨越两个子节点，并
计算每个算子的 `LeftRels/RightRels/LeftSchema/RightSchema/Rels/Schema/TES`。

规范前序编码定义为：

```
leaf     := relation name
internal := kind-token '(' left ',' right ')'
kind-token ∈ { 'I', 'L', 'S', 'A' }
```

代价相同时直接比较该字符串。

## 旋转规则

记 `a` 为后代算子，`b` 为祖先算子：

```
L-ASSCOM:  (X a Y) b Z  ->  (X b Z) a Y
R-ASSCOM:  (X a Y) b Z  ->  X a (Y b Z)
```

包内生成的冲突规则来自以下精确判定：

- `b` 移入 `a` 的左侧（保留侧）：L-ASSCOM 在原 `a` 是 `b` 左孩子时总是合法；
  当 `a` 是 `b` 右孩子时，仅 `b` 为 Inner 或 `b/a` 同为 LeftOuter 时合法。
- `b` 移入 `a` 的右侧（补空侧）：原 `a` 是 `b` 左孩子时，仅
  `a` 为 Inner，或 `a/b` 同为 LeftOuter 且谓词对 `a` 的补空侧 null-rejecting
  时合法；`a` 为 Semi/Anti 时禁止；原 `a` 是 `b` 右孩子时仅 Inner/Inner 合法。

非法旋转会生成对应的 `ConflictRule`。`Query.ConflictRules()` 可检查
`T1 -> T2` 关系集、方向（`ForbidLeft` / `ForbidRight`）和原因。

## 代价模型

默认 `DefaultCostModel` 是确定性的嵌套循环模型（`pairs=|L|*|R|`）：

```
matchedPairs = round(pairs * sel)
matchedLeft  = min(|L|, round(|L| * sel))
InnerJoin    : out = matchedPairs
LeftOuterJoin: out = matchedPairs + (|L| - matchedLeft)
SemiJoin     : out = matchedLeft
AntiJoin     : out = |L| - matchedLeft
localCost    = pairs
```

也可通过 `NewQueryWithCost` / `Input.Cost` 注入自定义 `CostModel`。
`Query.EnumerateUnrestricted()` 会把全部算子按 Inner 处理并忽略 TES/冲突
规则，用于得到全连接树枚举的代价下界。

## 验收测试

`go test ./...` 覆盖：

- 小表真实数据上，最优树与原语法树的全量结果等价；
- 与全连接（全部按 Inner 处理）枚举比较，冲突感知最小代价不低于下界；
- Inner + LeftOuter 的合法 L-ASSCOM 重排；
- AntiJoin 不可越过 LeftOuter 补空侧，但可移到保留侧；
- 两个 LeftOuter 在 null-rejecting 谓词下合法结合，在 null-preserving
  谓词下被精确禁止；
- 不可交换链只保留原顺序；
- 3 表查询与独立语义穷举的最优代价一致；
- 8 表混合连接在毫秒级完成枚举。
