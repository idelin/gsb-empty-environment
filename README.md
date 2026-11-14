# 函数事件被注入了伪造的消息

无服务器函数按事件中的队列与对象字段处理，攻击者直接调用函数端点并伪造事件结构，触发本不该执行的操作。请实现供Go函数运行时调用的可导入事件来源组件。事件必须来自登记的触发器，签名、主题、队列与函数绑定逐项校验；直接调用、跨函数事件与字段伪造拒绝。幂等键防止重复执行。用伪造事件、直接调用、跨函数触发、合法事件和重复投递验证只处理受信事件。

## 设计

`netsec` 包是供函数运行时导入的受信事件来源组件。函数的 HTTP 端点任何人
都可访问，因此组件绝不信任入站信封，而是在业务处理前完成一条完整校验管道
（见 `source.go` 的 `Source.Process`）：

1. 触发器必须在 `Registry` 中登记，否则拒绝（`ErrTriggerNotRegistered`）。
2. 事件必须携带 HMAC-SHA256 签名；无签名的直接调用拒绝
   （`ErrMissingSignature`），签名不匹配的伪造/篡改事件拒绝
   （`ErrInvalidSignature`）。
3. 签名覆盖全部攻击者可控字段（function/topic/queue/idempotency/object），
   采用长度前缀的规范化编码（`canonical.go`），防止跨字段拼接混淆。
4. 触发器必须绑定当前函数，事件自身声明的函数也必须与当前函数一致；跨函数
   事件分别返回 `ErrCrossFunction` 与 `ErrFunctionMismatch`。
5. 主题、队列必须与触发器登记的绑定逐项一致（`ErrTopicMismatch`、
   `ErrQueueMismatch`）。
6. 幂等键经原子的 `IdempotencyStore.CheckAndStore` 去重，重复投递返回
   `ErrDuplicateEvent`，并发重试也只执行一次。

校验通过后才向处理器交付 `VerifiedEvent`，业务代码不会接触未受信的
`InboundEvent`。

## 用法

```go
reg := netsec.NewRegistry()
_ = reg.Register(netsec.Trigger{
	ID:         "trig-a",
	FunctionID: "func-a",
	Topic:      "topic-a",
	Queue:      "queue-a",
	SigningKey:  busKey, // 与受信事件总线共享的 HMAC 密钥
})

src := netsec.NewSource("func-a", reg, netsec.NewMemoryIdempotencyStore())
http.Handle("/invoke", src.HTTPHandler(func(w http.ResponseWriter, r *http.Request) {
	event, ok := netsec.EventFromContext(r.Context()) // 已受信
	if !ok {
		http.Error(w, "untrusted", http.StatusForbidden)
		return
	}
	_ = event // 仅在此处处理受信事件
}))
```

签名与路由元数据通过带外请求头（`X-Event-Signature` 等）传输，头部值优先于
JSON 正文字段，使攻击者无法用伪造正文字段覆盖受信路由信息。

## 验证

`source_test.go` 覆盖：合法事件、无签名直接调用、伪造签名、签名后篡改
object/queue/topic/function/idempotency、跨函数触发、主题/队列绑定不符、
未登记触发器、缺失幂等键、重复投递、50 路并发重复只执行一次、HTTP 端到端
（含正文字段伪造被可信头覆盖）以及规范化编码的无碰撞性。

```
go test ./... -race -count=1
```
