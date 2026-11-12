# 采集页面只显示部分字段，缺失不一定表示值被删除

采集页面只显示部分字段，缺失不一定表示值被删除。写供Go数据接入宿主调用的观测合并包，每次抓取携带页面类型、覆盖字段集合、业务键和来源版本；只有明确覆盖字段的空值可删除旧值，未覆盖字段保留未知。不同来源按字段可信优先级合并，同优先冲突保留双方证据，不能以后到覆盖。支持撤回一次错误抓取并重算受影响键，用全部观测重放核对增量结果及每个输出字段来源。

## obsmerge 包

`obsmerge` 是供数据接入宿主调用的泛型观测合并包（Go 1.24），核心语义：

- 每次抓取用 `Observation[T]` 表达，携带 `PageType`、`CoveredFields`、
  `BusinessKey`、`SourceID/SourceVersion` 与摄入序号 `Seq`。
- 未出现在 `CoveredFields` 中的字段视为“本页未观测”，保持 `StatusUnknown`，
  不会被当成删除；只有被覆盖且显式为空（`obsmerge.Empty[T]()`，或覆盖但
  未在 `Fields` 中给出）才会产生可删除旧值的空证据。
- 来源按“字段 × 来源”可信优先级合并（`StaticPriority` / `FieldPriority`），
  高优先级胜出但低优先级证据仍保留在 `FieldView.Sources` 中。
- 同优先级冲突进入 `StatusConflict`，`Candidates` 保留双方全部证据，
  后到的抓取不会按时间覆盖先到的同优先级值。
- 同一来源的新抓取按 `Seq` 取代该来源旧证据；`Retract(id)` 撤回一次错误
  抓取，并用剩余观测只重算受影响的业务键。
- `ReplayAndVerify()` 用全部保留观测从零纯重放，与增量结果逐字段比对
  （含状态、值、全部来源证据），输出 `VerifyReport` 与每个输出字段的来源。

字段状态：`unknown`（从未覆盖）、`resolved`（单一最高优先证据）、
`deleted`（最高优先证据为显式空值）、`conflict`（同优先分歧，证据全保留）。

```go
eng := obsmerge.NewEngine[string](obsmerge.FieldPriority(0, nil, map[string]map[string]int{
	"name":  {"crm": 2},
	"email": {"billing": 2},
}))

_ = eng.Apply(obsmerge.Observation[string]{
	ID: "fetch-1", SourceID: "crm", SourceVersion: "2026-11-12",
	PageType: "profile", BusinessKey: "cust-7",
	CoveredFields: []string{"name"},
	Fields:        map[string]obsmerge.Nullable[string]{"name": obsmerge.Of("Acme")},
})

name, _ := eng.Field("cust-7", "name")   // resolved, Acme，来源可溯源
phone, _ := eng.Field("cust-7", "phone") // unknown：未覆盖不等于删除

_ = eng.Retract("fetch-1")               // 撤回错误抓取并重算该键
report, _ := eng.ReplayAndVerify()       // 全量重放核对增量结果
```

测试：`go test ./...`（含可运行示例与重放漂移检测）。
