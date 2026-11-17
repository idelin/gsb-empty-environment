# 写时复制页秘密清零

`example.org/netsec/q0257` 是可直接导入的 Go 页所有权包，用于快照内存宿主。

## 所有权模型

- 物理页由固定容量 `Pool` 管理，快照通过引用计数授权访问。
- `Snapshot.Fork` 复制逻辑页映射并增加每个共享物理页的引用。
- `Snapshot.WriteAt` 只对本次触及且仍被共享的页做写时复制；未触及页保持共享。
- `Snapshot.Close` / `Revoke` 撤销快照。只有引用数降到零的最后一页才进入清零。
- 清零期间页状态为 `PageZeroing`，既不可访问也不可分配；清零成功后才进入
  `PageFree`。
- 清零器报错、panic，或返回成功却没有把整页清零时，页进入
  `PageQuarantined`，永不重新分配。

所有权包内的页数据访问均受锁保护。`PageBytes` 返回副本；`Stats`、`PageInfo`
和 `Check` 可用于核对页状态与引用账。

## 清零器契约

```go
type Zeroizer interface {
    Zero(page []byte) error
}
```

`Zero` 必须同步完成，不得保留传入切片。清零器在页池锁之外串行调用，因此可以在
测试中暂停，模拟缓慢或阻塞的擦除；暂停只影响后续擦除，不阻塞分配和引用记账。
未提供清零器时使用 `ZeroBytes`。

## 示例

```go
pool, err := netsec.NewPoolWithPageSize(8, 3, nil)
if err != nil {
    log.Fatal(err)
}

base, err := pool.NewSnapshot([]byte("topsecret"))
if err != nil {
    log.Fatal(err)
}
branch, err := base.Fork()
if err != nil {
    log.Fatal(err)
}

// 只修改 base；branch 继续读取原始共享页。
if _, err := base.WriteAt([]byte("public"), 0); err != nil {
    log.Fatal(err)
}

original, _ := branch.Bytes()
fmt.Println(string(original)) // topsecret

_ = base.Close()   // branch 仍持有共享页，不能清零
_ = branch.Close() // 最后一个引用：清零并归还物理页
```

## 测试

测试使用 8/16 字节页和可暂停清零器，覆盖分叉、撤销、跨页写、并发写、
写与撤销竞态、清零失败隔离，以及每页内容和引用账：

```sh
go test ./...
go test -race ./...
```
