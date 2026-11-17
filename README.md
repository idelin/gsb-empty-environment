# 盲索引的跨字段关联

Go加密存储宿主需要可导入的盲索引包，禁止相同明文在不同租户或字段产生可关联索引。用HMAC-SHA256覆盖版本、租户、集合、字段和原始值的无歧义长度前缀编码，各域必须非空，值可为空；密钥由宿主句柄提供。轮换时查询同时返回当前和上一代索引，写入只用当前代；迁移条目必须携带可信版本见证，不接受客户端自报完成。退休上一代前所有已登记记录须有当前代索引，迁移与新写并发不能漏登记，退休后旧查询键拒绝。提供内存存储替身和确定调度测试跨字段关联、编码碰撞、迁移中新增、伪造完成见证及退休竞争，回调中不暴露明文。

## 用法

宿主通过 `KeyHandle` 按代际提供密钥。密钥不得进入索引请求，也不会由包内持久化。

```go
keys := netsec.KeyFunc(func(version netsec.Version) ([]byte, error) {
	return hostKeyFor(version)
})

manager, err := netsec.NewMemoryManager(keys, "v1")
if err != nil {
	return err
}

index, err := manager.Write("record-1", netsec.Record{
	Tenant:     "tenant-1",
	Collection: "people",
	Field:      "email",
	Value:      "person@example.test",
})
```

索引输入使用固定顺序的五段编码：版本、租户、集合、字段、原始值。每段先写 8 字节大端长度，再写原始字节，因此字段边界不会产生拼接碰撞。版本、租户、集合、字段必须非空，值可以为空。

## 轮换

```go
if err := manager.StartRotation("v2"); err != nil {
	return err
}

// 轮换期间查询返回 v2 和 v1 两个索引；写入只产生 v2。
query, err := manager.Query(tenant, collection, field, value)
current := query.Current
previous := query.Previous // 退休前非 nil

for _, entry := range manager.PendingMigrations() {
	// 宿主从自己的加密记录中取回原始值。
	if _, err := manager.Migrate(entry, record); err != nil {
		return err
	}
}

// 只有所有已登记记录都具备完全一致的当前代索引时才会成功。
if err := manager.RetirePrevious(); err != nil {
	return err
}
```

`MigrationEntry` 携带由旧代宿主密钥签发的版本见证，并绑定记录、旧索引和新代版本。`Migrate` 不接收“已完成”布尔值：它会先验证见证，再用调用方提供的原始值复算旧代索引，证明源数据与登记时一致，随后才写入并登记当前代索引。伪造、跨记录复用、过期代际见证和错误源数据都会被拒绝。

`Write`、`StartRotation`、`Migrate`、`RetirePrevious` 共用管理器锁，因此写入与轮换的线性化顺序明确；迁移期间的新写入直接落在当前代并登记。退休会逐条核对存储中的当前代索引；支持 `VersionDeleter` 的存储还会在同一转换中删除旧代条目。退休后 `QueryVersion` 对旧代返回 `ErrRetiredGeneration`。

`KeyHandle` 与 `Store` 回调在管理器锁内同步执行，宿主实现不应重入同一个 `Manager`。

## 存储替身

`MemoryStore` 提供并发安全的内存实现，`List` 以记录 ID 和代际确定排序。`Store` 回调接口只包含记录 ID 和 `Index`，不包含租户、字段或原始值。测试覆盖跨租户/集合/字段隔离、长度边界碰撞、迁移中新增、伪造见证、迁移与退休竞争以及回调明文泄漏。
