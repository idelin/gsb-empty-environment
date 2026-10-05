# objindex

内存对象索引库：新增、墓碑删除、按前缀分页列举，不依赖外部服务。

## 语义

- **新增 / 更新**：`Add(key, value)` 为键分配单调递增的世代号（generation）。
- **墓碑删除**：`Delete(key)` 保留墓碑，旧快照仍能判定该键已删除。
- **快照游标**：`Cursor` 绑定快照版本与最后位置 `(LastKey, LastGen)`；
  零值游标从当前版本开始新的列举。
- **删除重建**：同键删除后重建产生新世代，是不同对象，不会相互混淆。
- **限额**：`limit` 只计返回项；被过滤的墓碑不占用限额，但仍推进游标，
  因此过滤导致的空页也会前进（受 `WithMaxScan` 扫描上限约束）。
- **快照过期**：只保留最近 `WithRetainVersions` 个版本，过旧游标返回
  `ErrSnapshotExpired`。

## 示例

```go
ix := objindex.New(objindex.WithRetainVersions(64))
ix.Add("a/1", []byte("v1"))

var cur objindex.Cursor
for {
    page, err := ix.List("a/", cur, 100)
    if err != nil {
        return err
    }
    for _, obj := range page.Objects {
        fmt.Println(obj.Key, obj.Generation)
    }
    cur = page.Cursor
    if page.Done {
        break
    }
}
```

## 测试

```sh
go test -race ./...
```

测试以 `Freeze` 生成的冻结排序映射为参考，核对并发更新、空前缀、
最大键边界与快照过期等场景。
