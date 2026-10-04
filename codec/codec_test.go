package imgcodec

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// bruteRowCost 用纯递归穷举一行的所有指令划分，返回最小成本。
// 与编码器的 DP 相互独立，用于小图对照。
func bruteRowCost(row, prev []byte, cfg Config, i int) int {
	if i == len(row) {
		return 0
	}
	best := int(^uint(0) >> 2)
	// 字面段：任意长度。
	for l := 1; i+l <= len(row); l++ {
		if c := cfg.HeaderLiteral + l + bruteRowCost(row, prev, cfg, i+l); c < best {
			best = c
		}
	}
	// 重复段：相同字节的连续前缀。
	maxRun := 1
	for i+maxRun < len(row) && row[i+maxRun] == row[i] {
		maxRun++
	}
	for l := 1; l <= maxRun; l++ {
		if c := cfg.HeaderRepeat + 1 + bruteRowCost(row, prev, cfg, i+l); c < best {
			best = c
		}
	}
	// 上一行引用：受 MaxRefLen 与逐字节相等约束。
	if prev != nil {
		maxRef := 0
		for i+maxRef < len(row) && maxRef < cfg.MaxRefLen && row[i+maxRef] == prev[i+maxRef] {
			maxRef++
		}
		for l := 1; l <= maxRef; l++ {
			if c := cfg.HeaderRef + bruteRowCost(row, prev, cfg, i+l); c < best {
				best = c
			}
		}
	}
	return best
}

// streamCost 独立解析指令流并计算总成本，同时校验结构合法。
func streamCost(t *testing.T, data []byte, w, h int, cfg Config) int {
	t.Helper()
	total, pos, pixels := 0, 0, 0
	for pixels < w*h {
		if pos+instHeaderSize > len(data) {
			t.Fatalf("指令头越界: pos=%d", pos)
		}
		tag := InstTag(data[pos])
		n := int(binary.LittleEndian.Uint16(data[pos+1 : pos+3]))
		if n < 1 {
			t.Fatalf("指令长度为 %d", n)
		}
		if pixels%w+n > w {
			t.Fatalf("指令跨行: 列=%d 长=%d 宽=%d", pixels%w, n, w)
		}
		switch tag {
		case TagLiteral:
			total += cfg.HeaderLiteral + n
			pos += instHeaderSize + n
		case TagRepeat:
			total += cfg.HeaderRepeat + 1
			pos += instHeaderSize + 1
		case TagRef:
			if pixels/w == 0 {
				t.Fatalf("首行出现引用")
			}
			if n > cfg.MaxRefLen {
				t.Fatalf("引用长度 %d 超过上限 %d", n, cfg.MaxRefLen)
			}
			total += cfg.HeaderRef
			pos += instHeaderSize
		default:
			t.Fatalf("未知指令 %d", byte(tag))
		}
		pixels += n
	}
	if pos != len(data) {
		t.Fatalf("存在多余字节: 用了 %d / %d", pos, len(data))
	}
	return total
}

func bruteImageCost(img []byte, w, h int, cfg Config) int {
	total := 0
	for y := 0; y < h; y++ {
		row := img[y*w : (y+1)*w]
		var prev []byte
		if y > 0 {
			prev = img[(y-1)*w : y*w]
		}
		total += bruteRowCost(row, prev, cfg, 0)
	}
	return total
}

// TestEncodeMatchesBruteForce 对小图逐一枚举像素，对照编码成本与穷举最优。
func TestEncodeMatchesBruteForce(t *testing.T) {
	cfgs := []Config{
		{HeaderLiteral: 2, HeaderRepeat: 2, HeaderRef: 3, MaxRefLen: 2},
		{HeaderLiteral: 1, HeaderRepeat: 5, HeaderRef: 1, MaxRefLen: 3},
		{HeaderLiteral: 4, HeaderRepeat: 1, HeaderRef: 8, MaxRefLen: 1},
	}
	rng := rand.New(rand.NewSource(42))
	for _, cfg := range cfgs {
		for _, wh := range [][2]int{{1, 1}, {4, 1}, {5, 3}, {6, 2}} {
			w, h := wh[0], wh[1]
			for trial := 0; trial < 200; trial++ {
				img := make([]byte, w*h)
				for i := range img {
					// 小字母表 + 复制上一行，制造重复与引用机会。
					if rng.Intn(3) == 0 && i >= w {
						img[i] = img[i-w]
					} else {
						img[i] = byte(rng.Intn(3))
					}
				}
				data, err := Encode(img, w, h, cfg)
				if err != nil {
					t.Fatalf("Encode: %v", err)
				}
				got := streamCost(t, data, w, h, cfg)
				want := bruteImageCost(img, w, h, cfg)
				if got != want {
					t.Fatalf("cfg=%+v %dx%d img=%v: 编码成本 %d != 穷举最优 %d", cfg, w, h, img, got, want)
				}
				back, err := Decode(data, w, h, cfg)
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				if !bytes.Equal(back, img) {
					t.Fatalf("往返不一致: got %v want %v", back, img)
				}
			}
		}
	}
}

// TestRoundTripRandom 对较大的随机/结构化图像做往返测试。
func TestRoundTripRandom(t *testing.T) {
	cfg := DefaultConfig()
	rng := rand.New(rand.NewSource(7))
	for _, wh := range [][2]int{{1, 1}, {17, 5}, {64, 64}, {200, 3}} {
		w, h := wh[0], wh[1]
		img := make([]byte, w*h)
		for i := range img {
			switch rng.Intn(4) {
			case 0:
				img[i] = byte(rng.Intn(256))
			case 1:
				img[i] = 0xAB // 长重复段
			case 2:
				if i >= w {
					img[i] = img[i-w] // 与上一行一致
				}
			}
		}
		data, err := Encode(img, w, h, cfg)
		if err != nil {
			t.Fatalf("Encode %dx%d: %v", w, h, err)
		}
		back, err := Decode(data, w, h, cfg)
		if err != nil {
			t.Fatalf("Decode %dx%d: %v", w, h, err)
		}
		if !bytes.Equal(back, img) {
			t.Fatalf("%dx%d 往返不一致", w, h)
		}
	}
}

func inst(tag InstTag, n uint16, payload ...byte) []byte {
	b := []byte{byte(tag), 0, 0}
	binary.LittleEndian.PutUint16(b[1:], n)
	return append(b, payload...)
}

// TestDecodeStrictErrors 验证非法输入被定位到具体指令，且不返回像素。
func TestDecodeStrictErrors(t *testing.T) {
	cfg := Config{HeaderLiteral: 2, HeaderRepeat: 2, HeaderRef: 3, MaxRefLen: 4}
	w, h := 4, 2
	cases := []struct {
		name     string
		data     []byte
		wantInst int // 期望出错指令序号
	}{
		{"首行引用", inst(TagRef, 2), 0},
		{"引用超长", append(inst(TagLiteral, 4, 1, 2, 3, 4), inst(TagRef, 5)...), 1},
		{"引用跨行", append(inst(TagLiteral, 3, 1, 2, 3), inst(TagRef, 2)...), 1},
		{"字面段跨行", inst(TagLiteral, 5, 1, 2, 3, 4, 5), 0},
		{"未知指令", inst(InstTag(9), 1), 0},
		{"零长度", inst(TagLiteral, 0), 0},
		{"字面段负载截断", inst(TagLiteral, 4, 1, 2), 0},
		{"头部截断", []byte{byte(TagRepeat), 1}, 0},
		{"重复段缺负载", inst(TagRepeat, 2), 0},
		{"多余数据", append(append(inst(TagLiteral, 4, 1, 2, 3, 4), inst(TagLiteral, 4, 5, 6, 7, 8)...), 0xFF), 2},
		{"第二条指令非法", append(inst(TagLiteral, 4, 1, 2, 3, 4), inst(InstTag(7), 1)...), 1},
	}
	for _, c := range cases {
		out, err := Decode(c.data, w, h, cfg)
		if err == nil {
			t.Errorf("%s: 期望错误，却成功解码 %v", c.name, out)
			continue
		}
		if out != nil {
			t.Errorf("%s: 出错时不应返回像素，得到 %v", c.name, out)
		}
		de, ok := err.(*DecodeError)
		if !ok {
			t.Errorf("%s: 错误类型应为 *DecodeError，得到 %T", c.name, err)
			continue
		}
		if de.InstIndex != c.wantInst {
			t.Errorf("%s: 出错指令序号 = %d，期望 %d", c.name, de.InstIndex, c.wantInst)
		}
	}
}

// TestDecodeErrorOffset 验证错误偏移指向出错指令的起始字节。
func TestDecodeErrorOffset(t *testing.T) {
	cfg := DefaultConfig()
	first := inst(TagLiteral, 3, 9, 9, 9) // 3+3=6 字节
	data := append(first, inst(InstTag(7), 1)...)
	_, err := Decode(data, 3, 2, cfg)
	de, ok := err.(*DecodeError)
	if !ok {
		t.Fatalf("期望 *DecodeError，得到 %v", err)
	}
	if de.InstIndex != 1 || de.Offset != len(first) {
		t.Fatalf("定位错误: InstIndex=%d Offset=%d，期望 1/%d", de.InstIndex, de.Offset, len(first))
	}
}
