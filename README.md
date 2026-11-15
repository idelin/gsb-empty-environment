# 版本化盲索引

`netsec` 提供可直接导入的 Go 盲索引包，用于加密存储中的等值查询。索引为
HMAC-SHA256，输入是以下五个域的无歧义长度前缀编码：

```text
LP(decimal generation) || LP(tenant) || LP(collection) || LP(field) || LP(value)
```

`LP` 为 8 字节大端长度加原始字节。版本、租户、集合、字段必须非空，值允许
为空。相同明文在不同租户、集合或字段下不会产生相同索引。密钥由宿主的
`KeyHandle` 按代提供，包内不持久化或缓存密钥。

## 基本用法

```go
keys := netsec.KeyFunc(func(ctx context.Context, generation netsec.Generation) ([]byte, error) {
	return host.DecryptIndexKey(ctx, generation)
})

indexer, err := netsec.New(keys, 7)
if err != nil {
	return err
}

scope := netsec.Scope{
	Tenant:     "tenant-a",
	Collection: "users",
	Field:      "email",
}

// 写入只保存当前代索引，记录中不保留明文。
current, err := indexer.PutRecord(ctx, "user-42", scope, []byte("alice@example.com"))
if err != nil {
	return err
}

// 轮换期间查询同时使用当前代和上一代索引。
indexes, err := indexer.Query(ctx, scope, []byte("alice@example.com"))
```

## 轮换与迁移

1. `BeginRotation(ctx, next)` 激活下一代：写入只使用 `next`，查询返回
   `next` 与旧代两个索引。
2. 对尚未迁移的记录调用 `PrepareMigration`，宿主提供该记录的原始值。包会
   先用旧代索引核验值，再生成当前代索引和不可伪造的 `MigrationEntry` 见证。
3. `CommitMigration` 验证见证后才登记当前代索引。客户端无法通过布尔标志或
   自造条目声明迁移完成。
4. `RetirePrevious` 只有在存储中的每条已登记记录都有当前代索引时才成功。
   成功后旧索引被删除，`QueryGeneration` 对旧代返回
   `ErrRetiredGeneration`。

写入、迁移提交和退休共用生命周期读写锁。进行中的新写入和迁移提交持有读
锁，退休必须等它们完成后才能检查并切换状态，因此并发新增不会漏登记。

`RecordStore` 的 `Load`、`Store`、`List` 只接收 `StoredRecord`，其中只有记录
ID、作用域和索引，没有明文字段。包内提供并发安全的 `MemoryStore` 替身。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖跨租户/集合/字段关联、长度前缀编码碰撞、空值、轮换双读单写、迁移
期间新增、伪造或篡改见证、迁移与退休竞争、进行中写入与退休竞争，以及存储
回调不接收明文。
