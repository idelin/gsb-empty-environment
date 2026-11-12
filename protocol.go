package netsec

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
)

// DefaultMaxBodyBytes 是单次 PUTSCRIPT 脚本体的默认长度上限（10 MiB）。
const DefaultMaxBodyBytes = 10 * 1024 * 1024

// Rules 是站点对上传脚本的准入策略，由 PUTSCRIPT 调用方提供。
type Rules struct {
	// AllowedExtensions 是站点开放的 Sieve 扩展白名单，比较忽略大小写。
	// nil 或空表示不允许任何 require 扩展。
	AllowedExtensions []string
	// MaxRedirects 是单个脚本允许使用的 redirect 命令上限。
	MaxRedirects int
	// MaxBodyBytes 限制字面量声明的长度；<=0 时使用 DefaultMaxBodyBytes。
	MaxBodyBytes int
}

// ValidatedScript 是协议解析通过后得到的待保存内容。
// 此时脚本尚未落盘，调用方还需（或由 PutScript 代为）做内容校验与存储。
type ValidatedScript struct {
	// Name 是已解码并通过名称校验的脚本名。
	Name string
	// Body 是按字面量长度精确读出的脚本体，长度恒等于声明的 n。
	Body []byte
}

// ReadPutScript 从 ManageSieve 连接上读取并解析一条 PUTSCRIPT 命令。
//
// 安全要点：
//   - 脚本名经过 ValidateScriptName，名称中的 CRLF/引号无法注入命令；
//   - 脚本体为 {n} 或 {n+} 字面量时严格读取 n 个字节，不信任流内任何
//     长度之外的内容；
//   - n 字节之后只允许紧跟命令结束符 CRLF，同一连接里若出现额外字节
//     （例如攻击者拼接的 DELETESCRIPT）一律返回 ErrTrailingData，
//     这些字节不会被当作下一条命令解释，调用方应当关闭连接；
//   - 任何错误路径都不产生待保存内容。
func ReadPutScript(r *bufio.Reader, rules Rules) (*ValidatedScript, error) {
	maxBody := rules.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}

	line, err := r.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(line) == 0 {
		return nil, ErrTruncatedBody
	}
	// EOF 时最后一行若没有 LF，属于不完整命令。
	if line[len(line)-1] != '\n' {
		return nil, ErrTruncatedBody
	}

	name, body, parseErr := parsePutScriptLine(line, r, maxBody)
	if parseErr != nil {
		return nil, parseErr
	}
	if err := ValidateScriptName(name); err != nil {
		return nil, err
	}
	return &ValidatedScript{Name: name, Body: body}, nil
}

// parsePutScriptLine 解析 "PUTSCRIPT <name> <body>" 行；遇到字面量时
// 继续从 r 中按声明长度精确读取脚本体。
func parsePutScriptLine(line []byte, r *bufio.Reader, maxBody int) (string, []byte, error) {
	p := &lineScanner{buf: line}

	cmd, ok := p.token()
	if !ok || !strings.EqualFold(string(cmd), "PUTSCRIPT") {
		return "", nil, ErrProtocol
	}
	if !p.space() {
		return "", nil, ErrProtocol
	}

	name, ok := p.quoted()
	if !ok {
		return "", nil, ErrProtocol
	}

	if !p.space() {
		return "", nil, ErrProtocol
	}

	switch {
	case p.peek() == '"':
		body, ok := p.quoted()
		if !ok {
			return "", nil, ErrProtocol
		}
		if !p.expectCRLF() {
			return "", nil, ErrProtocol
		}
		if hasMore(r) {
			return "", nil, ErrTrailingData
		}
		return name, []byte(body), nil

	case p.peek() == '{':
		n, plus, ok := p.literalHeader()
		if !ok {
			return "", nil, ErrProtocol
		}
		if n < 0 || n > maxBody {
			return "", nil, ErrLiteralLength
		}
		// {n+} 表示同步字面量，服务端应先回响应再续读；PUTSCRIPT 入口
		// 不处理交互式续读，两种形式都按同一条命令内的字面量处理。
		_ = plus
		// 字面量头必须结束当前命令行；该 CRLF 已包含在 ReadBytes
		// 读出的 line 中，不属于脚本体，也不会再从 r 里重复读取。
		if !p.consumeCRLF() {
			return "", nil, ErrProtocol
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return "", nil, ErrTruncatedBody
		}
		// 字面量后必须是命令结束符 CRLF。
		term := make([]byte, 2)
		if _, err := io.ReadFull(r, term); err != nil {
			return "", nil, ErrTruncatedBody
		}
		if term[0] != '\r' || term[1] != '\n' {
			return "", nil, ErrProtocol
		}
		// 关键防线：n 字节之后的任何额外数据都是走私命令，绝不放行。
		if hasMore(r) {
			return "", nil, ErrTrailingData
		}
		return name, body, nil

	default:
		return "", nil, ErrProtocol
	}
}

// hasMore 判断读取器中是否还有未消费的字节。
// 不能用 Buffered()：ReadBytes 会把后续数据预读到内部缓冲。
func hasMore(r *bufio.Reader) bool {
	_, err := r.Peek(1)
	return !errors.Is(err, io.EOF)
}

// lineScanner 是单条命令行（含结尾 CRLF）上的小型解析器，只认
// RFC 5804 需要的带引号字符串与 {n[+]} 字面量头。
type lineScanner struct {
	buf []byte
	pos int
}

func (s *lineScanner) peek() byte {
	if s.pos >= len(s.buf) {
		return 0
	}
	return s.buf[s.pos]
}

func (s *lineScanner) space() bool {
	if s.peek() != ' ' {
		return false
	}
	s.pos++
	return true
}

// token 读取一个以空白分隔的原子（用于命令动词）。
func (s *lineScanner) token() ([]byte, bool) {
	start := s.pos
	for s.pos < len(s.buf) && s.buf[s.pos] != ' ' && s.buf[s.pos] != '\r' && s.buf[s.pos] != '\n' {
		s.pos++
	}
	return s.buf[start:s.pos], s.pos > start
}

// quoted 读取带引号字符串并解码 ManageSieve 转义（\" 与 \\）。
func (s *lineScanner) quoted() (string, bool) {
	if s.peek() != '"' {
		return "", false
	}
	s.pos++
	var b strings.Builder
	for {
		if s.pos >= len(s.buf) {
			return "", false
		}
		c := s.buf[s.pos]
		switch c {
		case '"':
			s.pos++
			return b.String(), true
		case '\\':
			s.pos++
			if s.pos >= len(s.buf) {
				return "", false
			}
			next := s.buf[s.pos]
			if next != '"' && next != '\\' {
				return "", false
			}
			b.WriteByte(next)
			s.pos++
		case '\r', '\n':
			// 原始控制字符不允许出现在引号字符串内。
			return "", false
		default:
			b.WriteByte(c)
			s.pos++
		}
	}
}

// literalHeader 解析 "{n}" 或 "{n+}"，返回 n、是否带 +。
func (s *lineScanner) literalHeader() (int, bool, bool) {
	if s.peek() != '{' {
		return 0, false, false
	}
	s.pos++
	digitsStart := s.pos
	for s.pos < len(s.buf) && s.buf[s.pos] >= '0' && s.buf[s.pos] <= '9' {
		s.pos++
	}
	if s.pos == digitsStart {
		return 0, false, false
	}
	digitsEnd := s.pos
	plus := false
	if s.peek() == '+' {
		plus = true
		s.pos++
	}
	if s.peek() != '}' {
		return 0, false, false
	}
	s.pos++ // 跳过 '}'
	n, err := strconv.Atoi(string(s.buf[digitsStart:digitsEnd]))
	if err != nil {
		return 0, false, false
	}
	return n, plus, true
}

// expectCRLF 要求行内剩余内容恰好为 CRLF。
func (s *lineScanner) expectCRLF() bool {
	if s.pos+2 != len(s.buf) {
		return false
	}
	return s.buf[s.pos] == '\r' && s.buf[s.pos+1] == '\n'
}

// consumeCRLF 消费字面量头之后的行结束 CRLF；该行已由 ReadBytes
// 整体读出，因此这里只做位置校验，不能再从连接读取。
func (s *lineScanner) consumeCRLF() bool {
	if s.pos+2 != len(s.buf) {
		return false
	}
	if s.buf[s.pos] != '\r' || s.buf[s.pos+1] != '\n' {
		return false
	}
	s.pos += 2
	return true
}
