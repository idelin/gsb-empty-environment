package bank

import (
	"fmt"

	"outboxsim/sim"
)

// Client 是命令发起方：发出命令后超时未收到响应就重投（至少一次）。
// 客户端本身不崩溃；收到响应的命令视为完成。
type Client struct {
	name string
	bus  *sim.Bus
	cmds []Command
	done map[string]Receipt
}

const clientTimeout int64 = 25

func NewClient(name string, bus *sim.Bus, cmds []Command) *Client {
	return &Client{name: name, bus: bus, cmds: cmds, done: map[string]Receipt{}}
}

// Done 报告命令是否都已收到响应。
func (c *Client) Done() bool { return len(c.done) == len(c.cmds) }

// DoneReceipts 返回已完成命令的响应。
func (c *Client) DoneReceipts() map[string]Receipt {
	out := make(map[string]Receipt, len(c.done))
	for k, v := range c.done {
		out[k] = v
	}
	return out
}

func (c *Client) Start(n *sim.Node) {
	c.bus.Handle(c.name, func(n *sim.Node, m *sim.Msg) {
		r, ok := m.Payload.(Receipt)
		if !ok {
			panic(fmt.Sprintf("%s: unexpected message", c.name))
		}
		// 重复响应直接忽略（命令本身可能早已完成）。
		if _, ok := c.done[r.CmdID]; ok {
			return
		}
		c.done[r.CmdID] = r
	})
	c.scheduleNext(n, 0)
}

func (c *Client) scheduleNext(n *sim.Node, idx int) {
	if idx >= len(c.cmds) {
		return
	}
	c.send(n, c.cmds[idx])
}

func (c *Client) send(n *sim.Node, cmd Command) {
	c.bus.Send(&sim.Msg{
		From: c.name, To: NodeSvc, Kind: "cmd",
		ID: cmd.CmdID, Payload: cmd,
	})
	// 未完成则在超时后重投；收到响应后这个定时器仅做无害检查。
	n.After(clientTimeout, func() {
		if _, ok := c.done[cmd.CmdID]; ok {
			c.scheduleNext(n, indexOf(c.cmds, cmd.CmdID)+1)
			return
		}
		n.Fault("client_timeout:" + cmd.CmdID) // 观测点（不武装则只是轨迹）
		c.send(n, cmd)
	})
}

func indexOf(cmds []Command, id string) int {
	for i := range cmds {
		if cmds[i].CmdID == id {
			return i
		}
	}
	return -1
}
