package netsec

import "crypto/sha256"

// Host 是验证宿主：内部持有增量解码器，仅在事件完整且摘要匹配时
// 才把事件摘要扩展进对应寄存器。寄存器初值为全零，扩展规则为
// reg = SHA-256(reg || 事件摘要)。
type Host struct {
	dec    *Decoder
	regs   map[uint8][DigestLen]byte
	events int
}

func NewHost(types *TypeTable) *Host {
	return &Host{dec: NewDecoder(types), regs: make(map[uint8][DigestLen]byte)}
}

// Feed 喂入下一块日志字节，返回本次扩展寄存器的事件数。
func (h *Host) Feed(p []byte) (int, error) {
	evs, err := h.dec.Feed(p)
	if err != nil {
		return 0, err
	}
	for _, ev := range evs {
		reg := h.regs[ev.Register]
		sum := sha256.New()
		sum.Write(reg[:])
		sum.Write(ev.Digest[:])
		var next [DigestLen]byte
		copy(next[:], sum.Sum(nil))
		h.regs[ev.Register] = next
		h.events++
	}
	return len(evs), nil
}

// Register 返回寄存器当前值；从未扩展过的寄存器为全零。
func (h *Host) Register(i uint8) [DigestLen]byte {
	return h.regs[i]
}

// Events 返回已成功扩展寄存器的事件总数。
func (h *Host) Events() int {
	return h.events
}

// Close 宣告日志结束；存在半个未交付事件时报错。
func (h *Host) Close() error {
	return h.dec.Close()
}
