package memo

// keyBuilder 是用于生成确定性字符串键的轻量构建器，避免在热路径上
// 反复进行字符串拼接带来的额外分配。
type keyBuilder struct {
	buf []byte
}

func newKeyBuilder() *keyBuilder {
	return &keyBuilder{}
}

func (b *keyBuilder) writeString(s string) {
	b.buf = append(b.buf, s...)
}

func (b *keyBuilder) writeByte(c byte) {
	b.buf = append(b.buf, c)
}

func (b *keyBuilder) writeInt(v int) {
	// 手动十进制编码，避免引入 strconv 仅用于此场景。
	if v == 0 {
		b.buf = append(b.buf, '0')
		return
	}
	var digits [20]byte
	pos := len(digits)
	for v > 0 {
		pos--
		digits[pos] = byte('0' + v%10)
		v /= 10
	}
	b.buf = append(b.buf, digits[pos:]...)
}

func (b *keyBuilder) string() string {
	return string(b.buf)
}
