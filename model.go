package acx

// maxTotal 是单个上下文 0/1 计数和的上限。
// 当某次更新后计数和恰好到达 maxTotal 时，两个计数各自向上取半（至少为 1）。
const maxTotal = 4096

// context 是单个上下文标签对应的二进制事件频率模型。
// 初始计数 zero = one = 1（拉普拉斯平滑），因此任何一位的概率永不等于 0 或 1。
type context struct {
	zero uint32 // 已观察到的 0 的计数
	one  uint32 // 已观察到的 1 的计数
}

func newContext() context { return context{zero: 1, one: 1} }

// total 返回计数和，即算术编码中使用的累积频率总量。
func (c *context) total() uint32 { return c.zero + c.one }

// update 在编码/解码一位之后更新该上下文（且只更新该上下文）。
// 编码器与解码器必须在完全相同的位置调用本函数，保证两侧模型同步。
func (c *context) update(bit uint8) {
	if bit == 0 {
		c.zero++
	} else {
		c.one++
	}
	if c.total() == maxTotal {
		// 各自向上取半；(x+1)>>1 对 x>=1 至少为 1。
		c.zero = (c.zero + 1) >> 1
		c.one = (c.one + 1) >> 1
	}
}
