package smtpdata

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"testing"
)

// chunkReader 按预设大小序列把 data 切成任意小块返回，模拟 TCP 分段。
type chunkReader struct {
	data    []byte
	sizes   []int
	pos     int
	idx     int
	rng     *rand.Rand // 可选：设置后每次 Read 按需生成 1..5 的块大小
	trailer []byte
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		if len(c.trailer) > 0 {
			n := copy(p, c.trailer)
			c.trailer = c.trailer[n:]
			return n, nil
		}
		return 0, io.EOF
	}
	size := len(p)
	if c.rng != nil {
		// 按需生成，避免预生成块数与实际读取次数不同步。
		size = 1 + c.rng.Intn(5)
	} else if c.idx < len(c.sizes) {
		size = c.sizes[c.idx]
		c.idx++
	}
	if size > len(p) {
		size = len(p)
	}
	if size > len(c.data)-c.pos {
		size = len(c.data) - c.pos
	}
	n := copy(p, c.data[c.pos:c.pos+size])
	c.pos += n
	return n, nil
}

func readAll(t *testing.T, r io.Reader, bufSize int) ([]byte, error) {
	t.Helper()
	out := make([]byte, 0, 64)
	buf := make([]byte, bufSize)
	for {
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			if err == io.EOF {
				return out, nil
			}
			return out, err
		}
	}
}

// ---- 独立参考实现：先在完整字节流中定位字面量标记，再整体解码 ----

func refFrame(data []byte) (body []byte, ok bool) {
	idx := bytes.Index(data, endMarker)
	if idx < 0 {
		return data, false
	}
	// 与 framingReader 一致：标记起点即末行 CRLF，需补回该 CRLF；
	// 标记位于流首（空邮件）时不产出空行。
	body = append([]byte{}, data[:idx]...)
	if idx > 0 {
		body = append(body, '\r', '\n')
	}
	return body, true
}

func refDecode(body []byte, cfg Config) ([]byte, *ProtocolError) {
	out := make([]byte, 0, len(body))
	lineStart := true
	for i := 0; i < len(body); i++ {
		b := body[i]
		switch {
		case b == '\r':
			if i+1 < len(body) && body[i+1] == '\n' {
				out = append(out, '\r', '\n')
				i++
				lineStart = true
			} else if cfg.BareCR == Normalize {
				out = append(out, '\r', '\n')
				lineStart = true
			} else {
				return out, &ProtocolError{Sequence: seqBareCR, Offset: int64(i)}
			}
		case b == '\n':
			if cfg.BareLF == Normalize {
				out = append(out, '\r', '\n')
				lineStart = true
			} else {
				return out, &ProtocolError{Sequence: seqBareLF, Offset: int64(i)}
			}
		case b == '.' && lineStart:
			lineStart = false
		default:
			lineStart = false
			out = append(out, b)
		}
	}
	return out, nil
}

type sample struct {
	name string
	data []byte // 含 CRLF.CRLF 结束标记的完整线路字节
}

var samples = []sample{
	{
		name: "clean",
		data: []byte("Subject: hi\r\n\r\nbody line 1\r\nbody line 2\r\n.\r\n"),
	},
	{
		name: "dot_stuffing",
		data: []byte("..dot line\r\n.a\r\n...three\r\nx..y\r\n.\r\n"),
	},
	{
		name: "smuggle_lf_dot_lf",
		// 伪造第二封邮件：裸 LF + 点号 + 裸 LF，绝不允许被识别为结束。
		data: []byte("first\r\n.\nMAIL FROM:<evil@example>\nRCPT TO:<victim>\nDATA\nfake\r\n.\r\n"),
	},
	{
		name: "smuggle_crcrlf",
		data: []byte("first\r\n.\r\r\nMAIL FROM:<evil@example>\r\r\nfake\r\n.\r\n"),
	},
	{
		name: "mixed_bare_endings",
		data: []byte("a\nb\r\nc\rd\re\nf\r\n.\r\n"),
	},
	{
		name: "marker_only",
		data: []byte("\r\n.\r\n"),
	},
	{
		name: "dot_then_bare_lf",
		data: []byte("x\r\n.\nrest\r\n.\r\n"),
	},
	{
		name: "crlf_at_eom",
		data: []byte("line\r\nline2\r\n.\r\n"),
	},
	{
		name: "binary_body",
		data: append(append([]byte{0x00, 0x01, '\n', 0xff, '\r', 0x7f}, "mid\r\n.\r\n"...), 0xaa),
	},
}

var configs = []struct {
	name string
	cfg  Config
}{
	{"strict", Config{}},
	{"normalize_lf", Config{BareLF: Normalize}},
	{"normalize_cr", Config{BareCR: Normalize}},
	{"normalize_all", Config{BareLF: Normalize, BareCR: Normalize}},
}

func outcomeEqual(got []byte, gotErr error, want []byte, wantErr *ProtocolError) bool {
	if wantErr != nil {
		var pe *ProtocolError
		if !errors.As(gotErr, &pe) {
			return false
		}
		if pe.Sequence != wantErr.Sequence || pe.Offset != wantErr.Offset {
			return false
		}
		// Reject 模式下，错误字节之前已合法产出的前缀会先交付，
		// 具体长度取决于缓冲/分块，但必须是参考完整输出的前缀且不越界。
		return bytes.HasPrefix(want, got) && len(got) <= len(want)
	}
	return gotErr == nil && bytes.Equal(got, want)
}

// TestAllSplitPoints 枚举每个样本的每个切分位置，要求流式结果与
// 参考实现逐字节一致、错误类型与偏移一致，且与分块方式无关。
func TestAllSplitPoints(t *testing.T) {
	for _, s := range samples {
		body, framed := refFrame(s.data)
		if !framed {
			t.Fatalf("sample %q missing end marker", s.name)
		}
		for _, c := range configs {
			wantOut, wantErr := refDecode(body, c.cfg)

			// 所有一刀两段的切分位置（0..len），覆盖标记在边界被拆开。
			for cut := 0; cut <= len(s.data); cut++ {
				sizes := []int{cut, len(s.data) - cut}
				for _, bufSize := range []int{1, 2, 3, 7, 64} {
					r := NewDataReader(&chunkReader{data: s.data, sizes: sizes}, c.cfg)
					got, err := readAll(t, r, bufSize)
					// Reject 模式在遇到歧义字节时提前报错，可能尚未读到
					// 结束标记，因此 Terminated 仅在无协议错误时要求为 true。
					if !outcomeEqual(got, err, wantOut, wantErr) ||
						(wantErr == nil && !r.Terminated()) {
						t.Fatalf("%s/%s cut=%d buf=%d: got %q err=%v term=%v; want %q err=%v",
							s.name, c.name, cut, bufSize, got, err, r.Terminated(), wantOut, wantErr)
					}
				}
			}

			// 纯单字节分段：每字节一次 Read，压测跨边界标记匹配。
			sizes := make([]int, len(s.data))
			for i := range sizes {
				sizes[i] = 1
			}
			r := NewDataReader(&chunkReader{data: s.data, sizes: sizes}, c.cfg)
			got, err := readAll(t, r, 1)
			if !outcomeEqual(got, err, wantOut, wantErr) {
				t.Fatalf("%s/%s byte-at-a-time: got %q err=%v; want %q err=%v",
					s.name, c.name, got, err, wantOut, wantErr)
			}

			// 随机分段，多种子重复。
			for seed := int64(0); seed < 20; seed++ {
				rng := rand.New(rand.NewSource(seed))
				cr := &chunkReader{data: s.data, rng: rng}
				r := NewDataReader(cr, c.cfg)
				got, err := readAll(t, r, 1+rng.Intn(8))
				if !outcomeEqual(got, err, wantOut, wantErr) {
					t.Fatalf("%s/%s rand seed=%d: got %q err=%v; want %q err=%v",
						s.name, c.name, seed, got, err, wantOut, wantErr)
				}
			}
		}
	}
}

// TestSmugglingCannotTerminate 安全断言：任何走私序列都不能提前结束 DATA，
// 伪造的第二封邮件必须完整保留在同一封正文里。
func TestSmugglingCannotTerminate(t *testing.T) {
	smuggled := []sample{samples[2], samples[3]}
	for _, s := range smuggled {
		for cut := 0; cut <= len(s.data); cut++ {
			r := NewDataReader(
				&chunkReader{data: s.data, sizes: []int{cut, len(s.data) - cut}},
				Config{BareLF: Normalize, BareCR: Normalize},
			)
			out, err := readAll(t, r, 4)
			if err != nil {
				t.Fatalf("%s cut=%d: unexpected err %v", s.name, cut, err)
			}
			if !r.Terminated() {
				t.Fatalf("%s cut=%d: not terminated", s.name, cut)
			}
			if !bytes.Contains(out, []byte("MAIL FROM:<evil@example>")) {
				t.Fatalf("%s cut=%d: smuggled content lost: %q", s.name, cut, out)
			}
			if bytes.Count(out, []byte("MAIL FROM")) != 1 {
				t.Fatalf("%s cut=%d: smuggled message split out: %q", s.name, cut, out)
			}
		}
	}
}

// TestMarkerTrailerNotConsumed 结束标记之后的字节必须留在底层流上，
// 供连接复用时的下一次读取获得。
func TestMarkerTrailerNotConsumed(t *testing.T) {
	data := []byte("body\r\n.\r\nNEXT MESSAGE BYTES")
	for cut := 0; cut <= len(data); cut++ {
		cr := &chunkReader{data: data, sizes: []int{cut, len(data) - cut}}
		r := NewDataReader(cr, Config{})
		out, err := readAll(t, r, 5)
		if err != nil || !r.Terminated() {
			t.Fatalf("cut=%d: err=%v terminated=%v", cut, err, r.Terminated())
		}
		if string(out) != "body\r\n" {
			t.Fatalf("cut=%d: body=%q", cut, out)
		}
		rest, rerr := io.ReadAll(cr)
		if rerr != nil || string(rest) != "NEXT MESSAGE BYTES" {
			t.Fatalf("cut=%d: trailer=%q err=%v", cut, rest, rerr)
		}
	}
}

// TestIncompleteStream 底层 EOF 但标记不完整时，已收正文照常投递，
// 随后返回 ErrIncomplete。
func TestIncompleteStream(t *testing.T) {
	for _, tail := range [][]byte{
		[]byte("partial\r\n.\r"), // 缺最后一个 LF
		[]byte("partial\r\n."),   // 标记被截断
		[]byte("partial\r\n"),    // 没有点号行
		[]byte("partial"),        // 行都不完整
		[]byte{},                 // 空流
	} {
		cfg := Config{BareLF: Normalize, BareCR: Normalize}
		r := NewDataReader(bytes.NewReader(tail), cfg)
		out, err := readAll(t, r, 3)
		if !errors.Is(err, ErrIncomplete) {
			t.Fatalf("tail=%q: want ErrIncomplete, got %q err=%v", tail, out, err)
		}
		wantBody, _ := refDecode(tail, cfg)
		if !bytes.Equal(out, wantBody) {
			t.Fatalf("tail=%q: got %q want %q", tail, out, wantBody)
		}
		if r.Terminated() {
			t.Fatalf("tail=%q: should not be terminated", tail)
		}
	}
}

// TestProtocolErrorOffsetStable 无论怎么分段，Reject 模式上报的偏移固定。
func TestProtocolErrorOffsetStable(t *testing.T) {
	data := []byte("aa\nbb\r\ncc\rdd\r\n.\r\n")
	body, _ := refFrame(data)
	_, wantLF := refDecode(body, Config{})
	_, wantCR := refDecode(body, Config{BareLF: Normalize})

	for cut := 0; cut <= len(data); cut++ {
		r := NewDataReader(&chunkReader{data: data, sizes: []int{cut, len(data) - cut}}, Config{})
		_, err := readAll(t, r, 2)
		var pe *ProtocolError
		if !errors.As(err, &pe) || pe.Offset != wantLF.Offset || pe.Sequence != wantLF.Sequence {
			t.Fatalf("LF cut=%d: err=%v want=%v", cut, err, wantLF)
		}

		r2 := NewDataReader(&chunkReader{data: data, sizes: []int{cut, len(data) - cut}},
			Config{BareLF: Normalize})
		_, err2 := readAll(t, r2, 2)
		var pe2 *ProtocolError
		if !errors.As(err2, &pe2) || pe2.Offset != wantCR.Offset || pe2.Sequence != wantCR.Sequence {
			t.Fatalf("CR cut=%d: err=%v want=%v", cut, err2, wantCR)
		}
	}
}

// TestLargeBodyPrefixRetention 验证长正文后标记跨块匹配不会误吞正文。
func TestLargeBodyPrefixRetention(t *testing.T) {
	payload := bytes.Repeat([]byte("abcdefgh\r\n"), 500)
	// payload 末行的 CRLF 复用为结束标记的前导 CRLF。
	data := append(append([]byte{}, payload...), []byte{'.', '\r', '\n'}...)
	cfg := Config{BareLF: Normalize, BareCR: Normalize}
	want, _ := refDecode(payload, cfg)

	rng := rand.New(rand.NewSource(42))
	cr := &chunkReader{data: data, rng: rng}
	r := NewDataReader(cr, cfg)
	got, err := readAll(t, r, 1+rng.Intn(8))
	if err != nil || !bytes.Equal(got, want) || !r.Terminated() {
		t.Fatalf("large body mismatch: got %d bytes want %d, err=%v term=%v",
			len(got), len(want), err, r.Terminated())
	}
}

// TestGoldenOutputs 锁定关键样本的规范化/拒绝结果，便于人工核对语义。
func TestGoldenOutputs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		cfg  Config
		want string
	}{
		{
			name: "strict_clean",
			raw:  "a\r\nb\r\n.\r\n",
			cfg:  Config{},
			want: "a\r\nb\r\n",
		},
		{
			name: "dot_unstuff",
			raw:  "..x\r\n...y\r\n.\r\n",
			cfg:  Config{},
			want: ".x\r\n..y\r\n",
		},
		{
			name: "normalize_lf_smuggle",
			raw:  "a\n.\nMAIL FROM:<x>\nfake\r\n.\r\n",
			cfg:  Config{BareLF: Normalize},
			// 行首单点按点填充规则剥去一个点；裸 LF 规范化为 CRLF，
			// 伪造内容仍留在同一封正文里，不会提前结束 DATA。
			want: "a\r\n\r\nMAIL FROM:<x>\r\nfake\r\n",
		},
		{
			name: "normalize_cr",
			raw:  "a\rb\r\n.\r\n",
			cfg:  Config{BareCR: Normalize},
			want: "a\r\nb\r\n",
		},
		{
			name: "marker_only_empty_body",
			raw:  "\r\n.\r\n",
			cfg:  Config{},
			want: "",
		},
	}
	for _, tc := range cases {
		for cut := 0; cut <= len(tc.raw); cut++ {
			r := NewDataReader(
				&chunkReader{data: []byte(tc.raw), sizes: []int{cut, len(tc.raw) - cut}},
				tc.cfg,
			)
			got, err := readAll(t, r, 1)
			if err != nil || string(got) != tc.want {
				t.Fatalf("%s cut=%d: got %q err=%v want %q", tc.name, cut, got, err, tc.want)
			}
		}
	}
}
