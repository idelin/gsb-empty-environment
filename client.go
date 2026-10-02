package causalrouter

import (
	"fmt"
	"sync"
)

// Client 维护本地版本向量的客户端。
// 发送时自增自身分量；接收（交付）时与消息时钟合并。
type Client struct {
	ID    string
	mu    sync.Mutex
	clock VectorClock
}

func NewClient(id string) *Client {
	return &Client{ID: id, clock: VectorClock{}}
}

// Send 生成一条新消息：本地分量 +1，并附上当前向量时钟。
func (c *Client) Send(payload string) *Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clock.Increment(c.ID)
	seq := c.clock[c.ID]
	return &Message{
		ID:      fmt.Sprintf("%s:%d", c.ID, seq),
		Sender:  c.ID,
		Clock:   c.clock.Copy(),
		Payload: payload,
	}
}

// Observe 在消息被交付时调用：合并消息时钟到本地版本向量。
func (c *Client) Observe(m *Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clock.Merge(m.Clock)
}

// Clock 返回本地版本向量的拷贝。
func (c *Client) Clock() VectorClock {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clock.Copy()
}
