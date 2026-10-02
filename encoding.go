package mpt

// keyToNibbles 将字符串键拆成 4-bit nibble 序列（每字节两个 nibble），
// 作为 Patricia 路径的基本单元。
func keyToNibbles(key string) []byte {
	b := []byte(key)
	nib := make([]byte, 0, len(b)*2)
	for _, c := range b {
		nib = append(nib, c>>4, c&0x0f)
	}
	return nib
}

// nibblesToKey 是 keyToNibbles 的逆操作，仅对偶数长度（完整键）有意义。
func nibblesToKey(nib []byte) string {
	if len(nib)%2 != 0 {
		return ""
	}
	b := make([]byte, len(nib)/2)
	for i := range b {
		b[i] = nib[2*i]<<4 | nib[2*i+1]
	}
	return string(b)
}

// compactEncode 实现 Hex-Prefix 紧凑编码（以太坊风格）：
// 高半字节为标志位：bit1 表示叶子，bit0 表示路径长度为奇数。
func compactEncode(nib []byte, leaf bool) []byte {
	flag := byte(0)
	if leaf {
		flag = 2
	}
	odd := len(nib)%2 == 1
	out := make([]byte, 0, len(nib)/2+1)
	rest := nib
	if odd {
		out = append(out, (flag+1)<<4|nib[0])
		rest = nib[1:]
	} else {
		out = append(out, flag<<4) // 低半字节为 0 填充
	}
	for i := 0; i+1 < len(rest); i += 2 {
		out = append(out, rest[i]<<4|rest[i+1])
	}
	return out
}

// compactDecode 解码 Hex-Prefix 编码，返回 nibble 路径与叶子标志。
func compactDecode(enc []byte) (nib []byte, leaf bool) {
	if len(enc) == 0 {
		return nil, false
	}
	flag := enc[0] >> 4
	leaf = flag >= 2
	odd := flag%2 == 1
	nib = make([]byte, 0, len(enc)*2)
	if odd {
		nib = append(nib, enc[0]&0x0f)
	}
	for _, b := range enc[1:] {
		nib = append(nib, b>>4, b&0x0f)
	}
	return nib, leaf
}

func commonPrefix(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

func equalNib(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// nibblesHex 以十六进制字符串展示 nibble 路径，用于调试输出。
func nibblesHex(nib []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(nib))
	for i, n := range nib {
		out[i] = digits[n&0x0f]
	}
	return string(out)
}
