# HTTP二代推送授权归属

代理转发服务器推送时，承诺流不能冒用另一个来源的认证上下文。写供Go接入宿主调用的推送准入包，承诺绑定父请求、连接代次与授权来源集合，伪头通过后才分配资源；父请求取消与推送取消分别记录，流号复用拒绝。用同连接多来源及迟到PUSH_PROMISE测试业务回调从不收到未经父上下文授权的资源。

## 接入方式

每条连接代次创建一个 `Admission`；每个父请求注册时快照其认证上下文允许的来源集合。收到 `PUSH_PROMISE` 后只调用 `AdmitPromise`，不要在准入前自行构造或分配推送资源。

```go
source, _ := netsec.ParseSource("https://a.example")

admission := netsec.NewAdmission(connectionGeneration,
	netsec.WithAllocator(host.AllocatePush),   // 伪头、来源、父上下文全部通过后才调用
	netsec.WithPushHandler(host.DeliverPush),  // 分配成功且推送进入 active 后才调用
)
defer admission.Close("connection closed")

parent, err := admission.RegisterParent(netsec.ParentSpec{
	StreamID:             parentStreamID,
	ConnectionGeneration: connectionGeneration,
	AuthorizedSources:    []netsec.Source{source},
	Context:              requestContext,
})
if err != nil {
	// 拒绝请求或关闭连接
}

push, err := admission.AdmitPromise(netsec.PromiseSpec{
	ParentStreamID:       parentStreamID,
	PromisedStreamID:     promisedStreamID,
	ConnectionGeneration: connectionGeneration,
	Headers:              pushPromiseHeaderFields,
})
```

安全行为：

- `:method/:scheme/:authority/:path` 必须完整、唯一且顺序合法；`GET/HEAD` 之外、连接级头、`host`、非法 URL 与 CR/LF 注入均拒绝。
- 承诺来源必须精确命中父请求注册时的来源快照；默认端口会规范化。承诺中的 `authorization`/`cookie` 不会成为新的认证上下文。
- 每个语法有效的承诺流号在首次出现时即写入墓碑；即使该承诺随后因来源、父请求或伪头失败，流号也不能换一个来源重放。流号必须在同一连接代次内严格递增。
- `CancelParent` 记录父请求取消并级联取消其推送；`CancelPush` 只记录单个推送取消。`Events`/`Stats` 将两类记录分开，`EndParent` 后的迟到 `PUSH_PROMISE` 也会被拒绝。

运行测试：

```sh
go test -race ./...
```
