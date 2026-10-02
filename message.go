package causalrouter

import "fmt"

// Message 携带向量时钟的离散消息。
type Message struct {
	ID      string      // 形如 "A:1" 的全局唯一事件标识
	Sender  string      // 发送方客户端ID
	Clock   VectorClock // 发送时刻的向量时钟（因果前置依赖）
	Payload string
}

func (m *Message) String() string {
	return fmt.Sprintf("%s clock=%s payload=%q", m.ID, m.Clock, m.Payload)
}
