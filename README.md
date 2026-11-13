# 逻辑复制行过滤器

`netsec` 是一个可导入的 Go 复制宿主组件，用于在 CDC/逻辑复制流中只发布满足租户谓词的行。它按稳定列 ID 处理 Relation 版本，使用事务私有缓存补齐 `UNCHANGED` 列，并保证：

- 回调只在 `Commit` 后收到输出；
- 回调不会收到谓词为 `false` 的完整行；
- `Delete` 只包含旧主键，不包含旧行其他列；
- `Abort` 丢弃全部缓冲输出和事务缓存；
- `UNCHANGED` 只能从相同 Relation 版本、相同主键的已认证缓存补齐。

## 基本接入

```go
const (
	colTenant  netsec.ColumnID = 1
	colID      netsec.ColumnID = 2
	colPayload netsec.ColumnID = 3
	colTOAST   netsec.ColumnID = 4
)

relation := netsec.Relation{
	ID:      42,
	Version: 7,
	Columns: []netsec.Column{
		{ID: colTenant, Name: "tenant", Type: "text"},
		{ID: colID, Name: "id", Type: "bigint"},
		{ID: colPayload, Name: "payload", Type: "text"},
		{ID: colTOAST, Name: "body", Type: "text", AllowUnchanged: true},
	},
	PrimaryKey: []netsec.ColumnID{colID},
}

filter, err := netsec.NewRowFilter(
	func(row netsec.Row) bool { return row[colTenant] == "A" },
	func(change netsec.Change) {
		// Insert/Update 的 change.Row 始终是完整且谓词为 true 的行。
		// Delete 的 change.Row 为 nil。
		// change.OldKey / change.Key 只包含主键列。
	},
)
if err != nil {
	panic(err)
}

_ = filter.Apply(netsec.RelationChange{Relation: relation})
_ = filter.Apply(netsec.Begin{XID: 100})
_ = filter.Apply(netsec.Insert{
	XID:        100,
	RelationID: relation.ID,
	Row: netsec.Row{
		colTenant: "A", colID: 1,
		colPayload: "hello", colTOAST: "large value",
	},
})
_ = filter.Apply(netsec.Commit{XID: 100})
```

也可以使用 `netsec.New(predicate, emit)`，其中
`predicate` 的类型是 `func(netsec.Relation, netsec.Row) bool`。
谓词必须是确定性、无副作用的纯函数。

## 事件语义

- `Insert`：`Row` 必须完整，不能包含 `Unchanged`。
- `Update`：`OldKey` 必须给出。`OldTuple` 可以部分给出；缺少的旧值会从精确的
  Relation 版本和旧主键缓存补齐。`NewTuple` 必须包含主键，并为每个非主键列
  提供值或 `netsec.Unchanged`。
- `Delete`：`OldTuple` 至少要包含主键；其余旧值可从缓存补齐。
- `RelationChange`：安装新版本，并按列 ID 迁移已提交缓存。
- `Begin` / `Commit` / `Abort`：事务由宿主显式管理，支持多个流式事务交错。

`Update` 输出分支固定如下：

| 旧行谓词 | 新行谓词 | 输出 |
| --- | --- | --- |
| true | true | `Update`，含 `OldKey`、`Key` 和完整新行 |
| true | false | `Delete`，只含 `OldKey` |
| false | true | `Insert`，含完整新行 |
| false | false | 不输出 |

## 需要补取

如果 `UNCHANGED` 列无法从同一版本、同一主键的已认证缓存补齐，或者主键改变
但新值没有显式提供，`Apply` 返回：

```go
var fetch *netsec.NeedsFetchError
if errors.As(err, &fetch) {
	// fetch.RelationID / fetch.Version / fetch.Key / fetch.Missing
	// 宿主补取后，用完整 tuple 重试同一事件。
}
```

失败事件不会修改事务私有缓存或缓冲输出，可以安全重试。缓存只保存谓词为
`true` 的行；新增列后的旧缓存可能缺少该列，但不会通过其他主键、其他租户的
缓存或旧 Relation 位置填补。

## Relation 版本迁移

迁移只按 `ColumnID` 对应列，不按列位置。新增列可以加入新版本，但迁移后的旧
缓存不会凭空产生新增列的值；需要时返回 `NeedsFetchError`。

以下情况拒绝自动迁移，并分别返回 `ErrColumnDeleted` 或 `ErrColumnReused`：

- 新版本删除了旧列 ID；
- 同一列 ID 的类型改变；
- 已删除的列 ID 再次出现。

如果宿主已经做了完整重同步，可以显式调用 `ResyncRelation` 清空该关系缓存并
开始新的列 ID 历史。

## 验证

```sh
go test ./...
go test -race ./...
```

测试覆盖两个租户、跨租户边界 `Update`、主键改变、未变 TOAST、流式事务交错、
`Abort`、关系版本重排/新增列，以及删除/重用列 ID 的拒绝路径。
