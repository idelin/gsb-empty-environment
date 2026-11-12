package netsec

import "fmt"

// Callback 是绑定到具体帧与具体导航代次的调用句柄。
//
// 页面脚本在文档 A 中拿到的原生对象回调,即使在导航到文档 B 之后
// 仍被旧闭包持有,调用时也会因为代次不再匹配而在闸门处被拒绝,
// 原生函数不会被触达。
type Callback struct {
	table      *CapabilityTable
	binding    string
	frameID    string
	generation Generation
}

// Binding 返回回调对应的绑定名。
func (c *Callback) Binding() string {
	return c.binding
}

// FrameID 返回回调绑定的帧标识。
func (c *Callback) FrameID() string {
	return c.frameID
}

// Generation 返回回调绑定时的导航代次。
func (c *Callback) Generation() Generation {
	return c.generation
}

// Call 在每次调用时复核当前帧的导航代次与来源,再决定是否派发原生函数。
//
// 复核顺序:
//  1. 回调必须仍在绑定它的帧上调用;
//  2. 帧当前导航代次必须与绑定时一致,导航后的旧回调立即失效;
//  3. 当前 (有效来源, 帧类型) 仍具备该绑定能力(授权可能已被撤销,
//     或帧被导航到了无权来源,例如 data: 文档)。
//
// 任一环节失败都返回错误且不执行 NativeFunc。
func (c *Callback) Call(frame *Frame, args []any) (any, error) {
	if frame == nil {
		return nil, fmt.Errorf("callback %q: frame must not be nil", c.binding)
	}
	snap := frame.Snapshot()

	if snap.FrameID != c.frameID {
		return nil, fmt.Errorf("callback %q bound to frame %q but invoked on frame %q: %w",
			c.binding, c.frameID, snap.FrameID, ErrFrameMismatch)
	}
	if snap.Generation != c.generation {
		return nil, fmt.Errorf("callback %q bound to generation %d but frame is now generation %d: %w",
			c.binding, c.generation, snap.Generation, ErrStaleGeneration)
	}

	c.table.mu.RLock()
	fn, err := c.table.lookupLocked(c.binding, snap)
	c.table.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return fn(args)
}
