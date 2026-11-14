# 租户隔离的分页游标

`example.org/netsec/q0293` 提供一个供 Go 列表服务导入的游标组件。游标由
HMAC-SHA256 认证，签名覆盖租户、规范化查询摘要、排序键、唯一键、偏移和
过期时间。客户端无法通过修改游标跨租户或跨筛选条件继续读取。

## 接入

```go
manager, err := netsec.NewCursorManager(netsec.CursorConfig{
    Secret:    []byte(os.Getenv("PAGINATION_HMAC_KEY")), // 生产环境建议至少 32 字节
    TTL:       5 * time.Minute,
    SingleUse: false, // true 时由该实例执行一次性消费检查
})
if err != nil {
    return err
}

// tenantID 必须来自服务端认证上下文，不能直接信任请求参数。
scope := netsec.Scope{
    TenantID: tenantID,
    Query: struct {
        Status string   `json:"status"`
        Order  []string `json:"order"`
    }{
        Status: filter.Status,
        Order:  []string{"created_at DESC", "id ASC"},
    },
}

position, ok := manager.ResumeOrStart(scope, request.Cursor)
if !ok {
    position = netsec.Position{Offset: 0} // 从第一页开始
}
```

`ResumeOrStart` 会在游标为空、被篡改、已过期、已消费或作用域不一致时返回
`ok=false`，不会返回其他租户或其他筛选条件的位置。需要明确返回
`400/409` 时改用 `Decode`，并用 `errors.Is` 区分
`ErrInvalidCursor`、`ErrExpiredCursor`、`ErrReplayedCursor`、
`ErrTenantMismatch` 和 `ErrQueryMismatch`。

`Scope.Query` 必须同时包含规范化后的筛选条件和排序定义；调用方应使用相同
的结构或 map 形状生成每一页的 scope。筛选条件变化后，摘要变化会使旧游标
失效。

## 排序键与下一页

`Position.SortKey` 保存所有非唯一排序表达式的值，`UniqueKey` 必须保存稳定
且租户内唯一的主键（复合主键可编码成一个规范值）。SQL 必须把该唯一键追加
为最后一个排序项：

```sql
SELECT id, tenant_id, status, created_at
FROM items
WHERE tenant_id = $1
  AND status = $2
  AND (created_at < $3 OR (created_at = $3 AND id > $4))
ORDER BY created_at DESC, id ASC
LIMIT $5;
```

下一页游标必须从本页最后一条记录生成：

```go
nextCursor, err := manager.Encode(scope, netsec.Position{
    SortKey:   []string{last.CreatedAt.UTC().Format(time.RFC3339Nano)},
    UniqueKey: last.ID,
    Offset:    position.Offset + int64(len(page)),
})
```

唯一键是必要的：仅有 `created_at` 等非唯一排序值时，相同值跨页会导致重复
或漏项。`Offset` 会签名保护并可用于进度审计，但翻页定位应使用排序键做
keyset 查询，而不是用偏移跳过租户数据。

默认游标有效期为 5 分钟。`SingleUse=true` 提供单实例内的一次性消费；多实例
部署若需要严格一次性语义，应使用共享存储实现重放检查，或保持短 TTL。

## 验证

```sh
go test ./...
go test -race ./...
```

测试覆盖跨租户复用与篡改、筛选条件变化、相同排序值跨页、过期、单次消费和
合法连续翻页。
