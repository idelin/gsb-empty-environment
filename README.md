# 机密索引比较运算预算

受限查询环境只能调用可信比较器，插件不得通过重复比较突破授权预算。写供Go查询宿主调用的比较门面包，每次比较绑定查询ID、字段与操作用途，预算先原子扣减，结果与幂等请求ID绑定；重放相同ID不同操作拒绝。用可重入比较器和并发请求验证不存在失败退额后的重复探测。

## 用法

```go
gate := netsec.NewScopedGate(
	func(ctx context.Context, req netsec.Request) (bool, error) {
		return trustedIndex.Match(ctx, req.QueryID, req.Field, req.Purpose, req.Input)
	},
	map[netsec.Scope]int{
		{QueryID: "q-42", Field: "email", Purpose: "exact-lookup"}: 8,
	},
)

matched, err := gate.Compare(ctx, netsec.Request{
	RequestID: "plugin-request-7",
	QueryID:   "q-42",
	Field:     "email",
	Purpose:   "exact-lookup",
	Input:     candidate,
})
```

查询宿主只把 `netsec.Comparer[T]` 接口暴露给插件，不把比较器或可修改预算的接口交给插件。

## 保证

- `RequestID`、`QueryID`、`Field`、`Purpose` 必须非空；操作标识同时包含 `Input`。
- 新请求先在互斥区内扣减预算，再调用可信比较器；预算不足时比较器不会被调用。
- 相同 `RequestID` 的并发请求只执行一次比较，并共享同一结果或错误。
- 相同 `RequestID` 搭配不同查询、字段、用途或输入时返回 `ErrRequestConflict`。
- 比较错误和比较器 panic 都占用预算且按 `RequestID` 缓存，不做失败退额。
- 比较器可重入调用门面；同 ID 重入返回 `ErrReentrantRequest`，避免自锁。

运行验证：

```sh
go test ./...
go test -race -count=10 ./...
```
