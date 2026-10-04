# snapshotpage

快照绑定的键集（keyset）分页库：按多个可空字段排序、唯一 ID 决胜，
翻页期间允许记录自由增删改，正反向翻页互逆，游标失效时返回明确错误。

## 设计

- **键集游标，不依赖行位置**：游标携带边界行的排序键值（各排序字段值 +
  唯一 ID），而不是偏移量或行引用。其他行（包括边界行本身）在翻页间隙被
  插入、更新或删除都不会使结果错位、重复或丢失。
- **快照绑定**：`Store` 持有快照 ID，游标内嵌该 ID。`Store.Reset()` 开启
  新快照后，旧游标一律以 `ErrSnapshotMismatch` 拒绝。
- **排序规则绑定**：游标内嵌 `SortSpec` 的 SHA-256 指纹，排序规则变更后
  旧游标以 `ErrSortMismatch` 拒绝；无法解码的 token 报 `ErrCursorMalformed`。
- **全序比较**：逐字段比较（空值位置 `NullsFirst`/`NullsLast` 显式指定且
  独立于升降序，类似 SQL `NULLS FIRST/LAST`），最后以唯一 ID 升序决胜，
  相同排序键的记录顺序确定。
- **正反向互逆**：正向取键大于边界的头 N 条；反向取键小于边界、最靠近
  边界的 N 条（按正向顺序返回）。每页返回 `Next`/`Prev` 游标，沿 `Next`
  前进再沿 `Prev` 回退精确还原每一页。

## 用法

```go
store := snapshotpage.NewStore()
store.Upsert(snapshotpage.Record{ID: "u1", Fields: map[string]any{"age": int64(30), "name": "ada"}})
// ... 更多 Upsert / Delete，翻页期间可随时调用

pager := snapshotpage.NewPaginator(store)
spec := snapshotpage.SortSpec{
    {Name: "age", Dir: snapshotpage.Desc, Nulls: snapshotpage.NullsLast},
    {Name: "name", Dir: snapshotpage.Asc, Nulls: snapshotpage.NullsFirst},
}

page, _ := pager.Page(snapshotpage.PageRequest{Spec: spec, Limit: 20})
next, _ := pager.Page(snapshotpage.PageRequest{Spec: spec, Limit: 20, After: page.Next})
back, _ := pager.Page(snapshotpage.PageRequest{Spec: spec, Limit: 20, Before: next.Prev})
// back.Items == page.Items
```

## 错误

| 错误 | 含义 |
| --- | --- |
| `ErrCursorMalformed` | token 无法解码或负载不完整 |
| `ErrSnapshotMismatch` | 游标属于其他快照（如 `Reset` 之后） |
| `ErrSortMismatch` | 游标的排序规则与本次请求不一致 |
| `ErrInvalidLimit` | `Limit <= 0` |
| `ErrConflictingCursors` | `After` 与 `Before` 同时设置 |

均可用 `errors.Is` 匹配。

## 字段值类型

字段值支持 `nil`（NULL）、`string`、`int64`、`float64`、`bool`；
游标序列化保留精确类型（`int64` 不会被 JSON 降级为浮点）。

## 测试

```sh
go test ./...
```

覆盖：空值四种排序组合、相同排序键 ID 决胜、多字段混合排序、正反向
互逆遍历、删除上页末行后续查、翻页期间增删改、三类游标失效错误。
