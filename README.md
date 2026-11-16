# Zanzibar快照令牌与撤销生效

关系授权系统读取权限时若用了比撤销更旧的快照，被移除的成员会因“新敌人问题”继续通过检查。为Go授权存储写可导入的快照一致性包，供检查接口调用：每次写入返回单调递增的 zookie（时间戳令牌）；检查请求可带 at_least_as_fresh 令牌，副本只有已应用到不低于该令牌时才可应答，否则等待或转发到领导者，不得用旧快照答复；资源内容变更在保存时记录写入产生的 zookie，对该资源的后续检查必须使用不低于它的快照；缺省检查用副本已应用的最新时间戳，但不得早于该资源最近一次 ACL 变更；撤销在其 zookie 之后的任何检查里立即生效；令牌格式非法拒绝，高于领导者最新值的令牌拒绝。用撤销后旧副本读取、资源内容令牌、未来令牌、非法令牌和副本落后验证。

## 包结构（package netsec）

- `zookie.go`：`Zookie` 令牌类型、HMAC-SHA256 签名与 `ParseZookie` 校验。
- `store.go`：领导者 `Store`，单调时钟、仅追加日志、每资源 ACL/内容新鲜度下限、领导者检查。
- `replica.go`：只读 `Replica`，按日志应用推进快照，等待或转发，绝不以旧快照应答。

## 一致性保证

- 每次 `WriteTuple` / `SaveContent` 都推进同一逻辑时钟并返回严格递增的
  `Zookie`（`v1:<timestamp>:<hmac>`，集群密钥签名，无法伪造）。
- `CheckRequest.Token` 即 `at_least_as_fresh`。副本仅在已应用时间戳
  `>= max(显式令牌, 该资源最新 ACL/内容 zookie)` 时才应答；否则先等待
  （`WithWaitTimeout`），超时后用 `WithForwarder` 转发领导者，两者皆无则
  返回 `ErrStaleReplica`，从不用旧快照答复。
- `SaveContent` 记录内容 zookie 作为资源新鲜度下限；后续检查不得早于它。
- 缺省检查使用副本最新已应用快照，但通过向领导者查询资源下限，保证不早于
  该资源最近一次 ACL 变更。
- 撤销写入的 zookie 之后的任何检查立即看到移除结果。
- 非法令牌返回 `ErrMalformedZookie`；时间戳高于领导者最新值的令牌返回
  `ErrFutureZookie`（即使签名合法）。

## 用法示例

```go
leader := netsec.NewStore(secret)
replica := netsec.NewReplica(leader,
    netsec.WithWaitTimeout(50*time.Millisecond),
    netsec.WithForwarder(leader.Check), // 落后时转发到领导者
)

grant, _ := leader.WriteTuple(netsec.RelationTuple{
    Object: "doc:1", Relation: "viewer", Subject: "user:a", Present: true,
})
replica.ApplyThrough(grant.Timestamp()) // 复制日志推进快照

res, err := replica.Check(ctx, netsec.CheckRequest{
    Object: "doc:1", Relation: "viewer", Subject: "user:a",
    Token: grant.Token(), // 可选的 at_least_as_fresh
})

revoke, _ := leader.WriteTuple(netsec.RelationTuple{
    Object: "doc:1", Relation: "viewer", Subject: "user:a", Present: false,
})
// 副本尚未应用 revoke 之前，对 doc:1 的检查会等待/转发/返回 ErrStaleReplica，
// 绝不会基于撤销前的旧快照放行。
_ = revoke
```

## 测试

`go test ./... -race` 覆盖：单调 zookie、撤销后旧副本读取、资源内容令牌、
未来令牌、非法令牌、副本等待与转发、领导者撤销即时生效。
