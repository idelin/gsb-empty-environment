# HTTP二代推送授权归属

代理转发服务器推送时，承诺流不能冒用另一个来源的认证上下文。写供Go接入宿主调用的推送准入包，承诺绑定父请求、连接代次与授权来源集合，伪头通过后才分配资源；父请求取消与推送取消分别记录，流号复用拒绝。用同连接多来源及迟到PUSH_PROMISE测试业务回调从不收到未经父上下文授权的资源。

## 方案

包 `netsec`（模块 `example.org/netsec/q0137`）为转发宿主提供一个会话级
HTTP/2 推送准入控制器，零第三方依赖，仅用标准库。

- **三方绑定**：每条 PUSH_PROMISE 在 `AdmitPromise` 中绑定父请求流、
  连接代次（generation）与承诺来源；来源经严格规范化（scheme/host/port）
  后与父请求 `BindParent` 登记的授权集合按值比较。
- **先校验后分配**：伪头块（RFC 7540 §8.1.2/8.2：四个必备伪头、顺序、
  重复、安全方法、连接级头、Host 一致性、可规范化的 authority）全部通过
  之前不写入任何每流状态，畸形承诺无法占用流号或放大内存。
- **代次与复用**：同一连接代次内承诺（偶数）流号不可复用；
  `AdvanceGeneration` 开启新一代次后旧代次的父请求与承诺全部失效，迟到或
  重放的 PUSH_PROMISE 返回 `ErrGenerationMismatch`，流号只允许在新一代复用。
- **取消分别记账**：`CancelParent` 与 `CancelPush` 分别产生独立事件与计数，
  二者均幂等；`Deliver` 在锁内复核存活状态，回调永远收不到已取消或代次
  陈旧的资源，且回调在锁外执行。
- **审计**：`Events()` 给出有序事件流，`Stats()` 给出准入/拒绝/交付/两类
  取消的计数快照；所有拒绝都带可 `errors.Is` 判定的哨兵错误。

## 宿主接入

```go
controller := netsec.NewController(businessHandler)
controller.BindParent(netsec.ParentRequest{
    StreamID:   1,
    Authorized: []netsec.Origin{origin},
})
admission, err := controller.AdmitPromise(netsec.PushPromise{ /* 帧内容 */ })
if err != nil { /* 记录拒绝并回 RST_STREAM，未分配任何资源 */ }
if err := admission.Deliver(ctx); err != nil { /* 取消/代次变化 */ }
```

## 测试

- `TestMultiOriginAuthorization`、`TestCallbackNeverReceivesUnauthorizedResource`：
  同连接多来源、跨上下文冒用、scheme/端口/子域差异，断言回调只收到本父上下文
  授权的资源。
- `TestLatePushPromise`：未知/已取消/已完成父请求，以及代次迁移后的迟到与
  重放承诺全部拒绝，旧承诺无法再交付。
- `TestSeparateCancelRecording`：父请求取消与推送取消独立计数、独立原因、
  幂等。
- `TestStreamIDReuse`：同代次流号复用拒绝，畸形块不占用流号，跨代次复用合法。
- `TestValidatePseudoHeader`、`TestOriginParsing`：伪头与来源规范化的表驱动
  用例。
- `TestConcurrentAdmission`：并发准入/交付/取消，配合 `go test -race`。

```sh
go test -race -count=1 ./...
```
