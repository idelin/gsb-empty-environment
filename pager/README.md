# pager — 快照键集分页库

按多个可空字段排序、以唯一编号决胜的游标分页。首页请求时物化**不可变有序快照**，
翻页期间的记录增删改不影响分页结果；续查按 `(排序键, 唯一编号)` 二分定位，
不依赖行号，也不要求游标对应的行仍然存在。

## 核心设计

- **快照绑定**：首页（无游标）请求时创建快照，物化全部行的排序键与记录副本并排序。
  游标携带快照编号，续查只读该快照。
- **游标内容**：`快照编号 + 排序规则指纹 + 方向 + 上页末行/首行的排序键与唯一编号`，
  JSON 序列化后 Base64 编码，对调用方不透明。
- **键集定位**：正向取快照中严格大于游标键的前 N 行，反向取严格小于游标键的后 N 行，
  因此正向/反向翻页互逆，且删掉上页末行不影响续查。
- **空值排序**：每字段可设 `NullsFirst` / `NullsLast`；默认遵循 SQL 惯例
  （升序空值最后、降序空值最前），空值位置不随方向反转。
- **决胜规则**：排序键相同一律按唯一编号升序，保证全序。

## 错误

| 错误 | 含义 |
|---|---|
| `ErrCursorMalformed` | 游标无法解码或结构非法 |
| `ErrSnapshotExpired` | 游标绑定的快照不存在或已释放 |
| `ErrSortMismatch` | 请求排序规则与游标绑定的不一致 |
| `ErrDirectionMismatch` | `NextCursor`/`PrevCursor` 与请求方向不匹配 |
| `ErrInvalidPageSize` / `ErrNoSortFields` | 请求参数非法 |

## 用法

```go
store := pager.NewStore()
store.Put(pager.Record{ID: "u1", Fields: map[string]pager.Value{
    "city": pager.String("sh"), "age": pager.Int(30)}})

p := pager.NewPaginator(store)
sort := pager.SortSpec{pager.Asc("city"), pager.Desc("age")}

page, _ := p.Page(pager.PageRequest{Sort: sort, Size: 20})            // 首页（建快照）
next, _ := p.Page(pager.PageRequest{Sort: sort, Size: 20,
    Cursor: page.NextCursor})                                        // 正向续查
prev, _ := p.Page(pager.PageRequest{Sort: sort, Size: 20,
    Cursor: next.PrevCursor, Dir: pager.Backward})                   // 反向翻回

store.ReleaseSnapshot(page.SnapshotID) // 主动释放快照
```

## 测试

```sh
GO111MODULE=on go test ./...
```

覆盖：空值四种排序组合、相同排序键决胜遍历、删掉上页末行后续查、
正反向互逆、反向从尾部起翻、各类游标失效报错、快照与实时写隔离。
