package netsec

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
)

// Framing 描述一个响应正文在连接上的定界方式。
type Framing int

const (
	// FramingNone 表示该响应没有正文（HEAD、1xx、204、304 等）。
	FramingNone Framing = iota
	// FramingLength 表示正文由 Content-Length 精确界定。
	FramingLength
	// FramingChunked 表示正文使用 chunked 传输编码。
	FramingChunked
	// FramingClose 表示没有长度字段，正文一直持续到连接关闭。
	FramingClose
	// FramingTunnel 表示 CONNECT 2xx，连接自此交给隧道，不再解析 HTTP。
	FramingTunnel
)

func (f Framing) String() string {
	switch f {
	case FramingNone:
		return "none"
	case FramingLength:
		return "content-length"
	case FramingChunked:
		return "chunked"
	case FramingClose:
		return "close-delimited"
	case FramingTunnel:
		return "tunnel"
	default:
		return "unknown"
	}
}

var (
	// ErrTruncated 表示定长或 chunked 正文在完整接收前连接就被对端关闭，
	// 即正文被截断。出现该错误后连接禁止复用。
	ErrTruncated = errors.New("netsec: response body truncated before declared length")
	// ErrProtocol 表示线上消息违反 HTTP 报文分帧规则（畸形起始行、
	// 非法 Content-Length、同时出现 Content-Length 与 Transfer-Encoding 等）。
	ErrProtocol = errors.New("netsec: malformed HTTP response framing")
)

// Response 是序列中的一个 HTTP 响应。1xx 临时响应也会作为独立的 Response 返回，
// 其 Framing 为 FramingNone、Body 为 http.NoBody。
type Response struct {
	ProtoMajor    int
	ProtoMinor    int
	StatusCode    int
	Status        string
	Header        http.Header
	Framing       Framing
	ContentLength int64
	// Body 的读取严格受 Framing 约束：读取不会越过该响应自身的正文边界。
	// 读完或调用 Close 后，同一连接上的后续响应才能被读取。
	Body io.ReadCloser
}

// Reader 在一条持久连接上按顺序读取响应。它按请求方法与状态码决定每个响应
// 是无正文、定长、chunked 还是以连接关闭定界，保证同一连接上的后续响应
// 不会被误当作前一个响应的正文。
type Reader struct {
	br           *bufio.Reader
	tr           *textproto.Reader
	reusable     bool
	bodyComplete bool
}

// NewReader 在底层连接（或任意 io.Reader）之上创建响应序列读取器。
func NewReader(r io.Reader) *Reader {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return &Reader{
		br:           br,
		tr:           textproto.NewReader(br),
		reusable:     true,
		bodyComplete: true,
	}
}

// BufferedReader 返回底层带缓冲读取器。隧道切换后，转发宿主应先通过它取走
// 已缓冲的字节，再直接在原始连接上收发，避免丢失隧道前导数据。
func (r *Reader) BufferedReader() *bufio.Reader {
	return r.br
}

// Reusable 报告当前连接是否还能承载下一个响应。进入关闭定界、隧道模式，
// 或发生正文截断/协议错误后返回 false。
func (r *Reader) Reusable() bool {
	return r.reusable
}

// ReadResponse 读取序列中的下一个响应。method 是产生该响应的请求方法
// （如 HEAD、CONNECT），用于判断 HEAD 无正文与 CONNECT 隧道。
// 连接在无数据时干净关闭返回 io.EOF；在不可复用的连接上继续读取返回错误。
func (r *Reader) ReadResponse(method string) (*Response, error) {
	if !r.bodyComplete {
		return nil, errors.New("netsec: previous response body is not fully read or closed")
	}
	if !r.reusable {
		return nil, errors.New("netsec: connection cannot be reused after close-delimited body, tunnel, or truncation")
	}

	statusLine, err := r.tr.ReadLine()
	if err != nil {
		return nil, err
	}
	major, minor, code, status, err := parseStatusLine(statusLine)
	if err != nil {
		r.reusable = false
		return nil, err
	}
	mimeHeader, err := r.tr.ReadMIMEHeader()
	if err != nil {
		r.reusable = false
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrTruncated
		}
		return nil, err
	}
	header := http.Header{}
	for k, vv := range mimeHeader {
		header[k] = vv
	}

	connClose := wantsClose(major, minor, header)

	// 1xx 信息响应：没有正文，也不改变连接的可复用状态。
	if code >= 100 && code < 200 {
		return &Response{
			ProtoMajor: major, ProtoMinor: minor, StatusCode: code, Status: status,
			Header: header, Framing: FramingNone, ContentLength: 0, Body: http.NoBody,
		}, nil
	}

	framing, length, err := frameFor(method, code, header)
	if err != nil {
		r.reusable = false
		return nil, err
	}

	resp := &Response{
		ProtoMajor: major, ProtoMinor: minor, StatusCode: code, Status: status,
		Header: header, Framing: framing, ContentLength: length,
	}

	switch framing {
	case FramingNone:
		resp.Body = http.NoBody
		r.bodyComplete = true
	case FramingLength:
		resp.Body = &lengthBody{br: r.br, owner: r, remaining: length}
		r.bodyComplete = false
	case FramingChunked:
		resp.Body = &chunkedBody{br: r.br, owner: r}
		r.bodyComplete = false
	case FramingClose:
		resp.Body = &closeBody{br: r.br, owner: r}
		r.bodyComplete = false
		r.reusable = false
	case FramingTunnel:
		resp.Body = &tunnelBody{br: r.br}
		r.bodyComplete = false
		r.reusable = false
	}

	if connClose {
		r.reusable = false
	}
	return resp, nil
}

func parseStatusLine(line string) (major, minor, code int, status string, err error) {
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
		return 0, 0, 0, "", ErrProtocol
	}
	version := strings.SplitN(strings.TrimPrefix(parts[0], "HTTP/"), ".", 2)
	if len(version) != 2 {
		return 0, 0, 0, "", ErrProtocol
	}
	major, err = strconv.Atoi(version[0])
	if err != nil {
		return 0, 0, 0, "", ErrProtocol
	}
	minor, err = strconv.Atoi(version[1])
	if err != nil {
		return 0, 0, 0, "", ErrProtocol
	}
	code, err = strconv.Atoi(parts[1])
	if err != nil || code < 100 || code > 999 {
		return 0, 0, 0, "", ErrProtocol
	}
	if len(parts) == 3 {
		status = parts[2]
	}
	return major, minor, code, status, nil
}

// wantsClose 按 HTTP 版本与 Connection 头判断该响应后连接是否关闭。
func wantsClose(major, minor int, h http.Header) bool {
	if major != 1 {
		return true
	}
	switch minor {
	case 0:
		return !hasHeaderToken(h, "Connection", "keep-alive")
	default: // HTTP/1.1 及更高小版本默认保持连接
		return hasHeaderToken(h, "Connection", "close")
	}
}

func hasHeaderToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// frameFor 依据请求方法、状态码与长度相关头部决定正文分帧方式。
func frameFor(method string, code int, h http.Header) (Framing, int64, error) {
	switch {
	case code == 304:
		return FramingNone, 0, nil
	case code == 204:
		return FramingNone, 0, nil
	case strings.EqualFold(method, http.MethodHead):
		// HEAD 响应的头部可能带有 Content-Length，但线上没有任何正文字节。
		return FramingNone, 0, nil
	case strings.EqualFold(method, http.MethodConnect) && code >= 200 && code < 300:
		return FramingTunnel, 0, nil
	}

	if te := h.Values("Transfer-Encoding"); len(te) > 0 {
		last := te[len(te)-1]
		if idx := strings.LastIndexByte(last, ','); idx >= 0 {
			last = last[idx+1:]
		}
		if strings.EqualFold(strings.TrimSpace(last), "chunked") {
			// 同时携带 Content-Length 是典型的请求走私风险，直接判协议错误。
			if len(h.Values("Content-Length")) > 0 {
				return 0, 0, ErrProtocol
			}
			return FramingChunked, 0, nil
		}
		// 非 chunked 的传输编码无法自定界，只能靠关闭连接界定正文。
		return FramingClose, 0, nil
	}

	if cl := h.Values("Content-Length"); len(cl) > 0 {
		length, err := parseContentLength(cl)
		if err != nil {
			return 0, 0, err
		}
		return FramingLength, length, nil
	}

	// 没有任何长度字段：正文持续到连接关闭，连接不可复用。
	return FramingClose, 0, nil
}

func parseContentLength(values []string) (int64, error) {
	var first string
	for i, v := range values {
		s := strings.TrimSpace(v)
		if strings.ContainsAny(s, ", ") {
			// 拒绝逗号合并或夹带空白的复合值，避免走私歧义。
			return 0, ErrProtocol
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return 0, ErrProtocol
		}
		s = strconv.FormatInt(n, 10)
		if i == 0 {
			first = s
		} else if s != first {
			// 多个互相矛盾的 Content-Length 一律拒绝。
			return 0, ErrProtocol
		}
	}
	n, _ := strconv.ParseInt(first, 10, 64)
	return n, nil
}
