# AMQP凭据刷新信道复核

AMQP 0-9-1代理在connection.update-secret刷新凭据后，已打开的信道仍沿用旧授权缓存，攻击者借此继续消费已被撤销的队列。给Go代理写供信道操作调用的授权上下文包：每个信道缓存的configure、write、read权限带凭据代次，刷新后下一次操作必须重算，已有消费者在下一次投递前复核，失败则取消该消费者；信道号关闭后重开不继承旧delivery tag，旧tag的ack和nack按未知投递处理。用刷新与投递交错、重用信道号和同连接多信道验证。

## 实现

- `Connection.UpdateSecret` 原子推进凭据代次，所有信道共享该代次。
- 每个 `Channel` 按 `(access, resource)` 缓存 `configure`、`write`、`read` 决策，并记录产生该决策的凭据代次；代次不匹配时不会使用缓存。
- 授权计算与刷新交错时，旧代次结果会被丢弃并重试；投递的授权检查与delivery tag分配通过连接级代次闸门线性化。刷新后的下一次投递不能沿用旧授权。
- `Deliver` 在每次投递前重新执行消费者的 `read` 检查；拒绝时删除消费者，不分配delivery tag，并返回同时匹配 `ErrConsumerCancelled` 和 `ErrAccessRefused` 的错误。
- 信道关闭会丢弃消费者、授权缓存、未结算投递和delivery tag计数器。重开同一信道号会得到新epoch，tag从1重新开始；旧tag的ack/nack返回 `ErrUnknownDeliveryTag`。
- 同一连接上的多个信道拥有独立缓存、消费者和tag空间，但共享凭据代次。

## API 示例

```go
import "example.org/netsec/q0106"

func example(payload []byte) error {
authz := netsec.AuthorizerFunc(func(cred netsec.Credential, res netsec.Resource, access netsec.Access) bool {
	// 根据 cred.Generation / cred.Secret 和 res / access 计算权限。
	return false
})

conn := netsec.NewConnection("conn-1", "secret-v1", authz)
ch, err := conn.OpenChannel(1)
if err != nil {
	return err
}
if err := ch.DeclareQueue("orders"); err != nil {
	return err
}
if _, err := ch.Consume("orders", "worker"); err != nil {
	return err
}

delivery, err := ch.Deliver("worker", payload)
if err != nil {
	return err
}
if err := ch.Ack(delivery.Tag); err != nil {
	return err
}

// connection.update-secret：下一操作及下一次投递必须按新凭据重算。
conn.UpdateSecret("secret-v2")
return nil
}
```

## 验证

`go test ./...` 与 `go test -race ./...` 覆盖刷新与投递交错、授权计算过程中发生刷新、信道号关闭后重用、旧delivery tag的ack/nack，以及同连接多信道的独立授权和tag状态。
