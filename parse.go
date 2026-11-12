package taskpkg

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

const maxInt = int(^uint(0) >> 1)

// ParseStrict applies the boundary policy required by the task.
func ParseStrict(methods []string, raw []byte) ParseResult {
	return parseResponses(methods, append([]byte(nil), raw...), "strict", false)
}

// ParseLenient is a deliberately different strategy. It accepts chunked
// transfer coding, repeated Content-Length values (first value wins), and a
// connection closing before a declared length is reached. It is useful as a
// ready-made alternate parser for fuzz and test-host callers.
func ParseLenient(methods []string, raw []byte) ParseResult {
	return parseResponses(methods, append([]byte(nil), raw...), "lenient", true)
}

func methodAt(methods []string, idx int) string {
	if idx < 0 || idx >= len(methods) {
		return ""
	}
	return methods[idx]
}

func isHeadMethod(method string) bool {
	return strings.EqualFold(method, "HEAD")
}

func readLine(raw []byte, pos int) (line []byte, next int, ok bool) {
	if pos < 0 || pos >= len(raw) {
		return nil, pos, false
	}
	rel := bytes.IndexByte(raw[pos:], '\n')
	if rel < 0 {
		return nil, pos, false
	}
	lineEnd := pos + rel
	if lineEnd > pos && raw[lineEnd-1] == '\r' {
		line = raw[pos : lineEnd-1]
	} else {
		line = raw[pos:lineEnd]
	}
	return line, lineEnd + 1, true
}

func parseStatusLine(raw []byte, start int) (protocol string, code int, reason string, headerStart int, perr *ParseError) {
	line, next, ok := readLine(raw, start)
	if !ok {
		return "", 0, "", start, &ParseError{
			Kind:         ErrStatusLine,
			Offset:       len(raw),
			DetailOffset: start,
			Message:      "status line is not terminated by a newline",
		}
	}
	if len(line) == 0 {
		return "", 0, "", next, &ParseError{
			Kind:         ErrStatusLine,
			Offset:       start,
			DetailOffset: start,
			Message:      "empty status line",
		}
	}
	sp := bytes.IndexByte(line, ' ')
	if sp <= 0 {
		return "", 0, "", next, &ParseError{
			Kind:         ErrStatusLine,
			Offset:       start,
			DetailOffset: start,
			Message:      "status line has no protocol/code separator",
		}
	}
	protocol = string(line[:sp])
	if !strings.HasPrefix(protocol, "HTTP/") || len(protocol) <= len("HTTP/") {
		return "", 0, "", next, &ParseError{
			Kind:         ErrStatusLine,
			Offset:       start,
			DetailOffset: start,
			Message:      fmt.Sprintf("unsupported protocol %q", protocol),
		}
	}
	rest := bytes.TrimLeft(line[sp+1:], " \t")
	if len(rest) < 3 {
		return "", 0, "", next, &ParseError{
			Kind:         ErrStatusLine,
			Offset:       start,
			DetailOffset: start,
			Message:      "status code is missing",
		}
	}
	codeEnd := 0
	for codeEnd < len(rest) && rest[codeEnd] != ' ' && rest[codeEnd] != '\t' {
		codeEnd++
	}
	codeToken := rest[:codeEnd]
	if len(codeToken) != 3 || !isDecimal(codeToken) {
		return "", 0, "", next, &ParseError{
			Kind:         ErrStatusLine,
			Offset:       start,
			DetailOffset: start,
			Message:      fmt.Sprintf("invalid status code %q", codeToken),
		}
	}
	code, _ = strconv.Atoi(string(codeToken))
	if code < 100 || code > 999 {
		return "", 0, "", next, &ParseError{
			Kind:         ErrStatusLine,
			Offset:       start,
			DetailOffset: start,
			Message:      fmt.Sprintf("status code %d out of range", code),
		}
	}
	reason = string(bytes.TrimLeft(rest[codeEnd:], " \t"))
	return protocol, code, reason, next, nil
}

func parseHeaderLine(line []byte, off int) (Header, *ParseError) {
	if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
		return Header{}, &ParseError{
			Kind:         ErrHeaderLine,
			Offset:       off,
			DetailOffset: off,
			Message:      "obsolete header line folding",
		}
	}
	colon := bytes.IndexByte(line, ':')
	if colon <= 0 {
		return Header{}, &ParseError{
			Kind:         ErrHeaderLine,
			Offset:       off,
			DetailOffset: off,
			Message:      "header field has no name/value separator",
		}
	}
	name := line[:colon]
	if !isToken(name) {
		return Header{}, &ParseError{
			Kind:         ErrHeaderLine,
			Offset:       off,
			DetailOffset: off,
			Message:      fmt.Sprintf("invalid header name %q", name),
		}
	}
	value := bytes.Trim(line[colon+1:], " \t")
	for _, b := range value {
		if b < 0x20 && b != '\t' {
			return Header{}, &ParseError{
				Kind:         ErrHeaderLine,
				Offset:       off,
				DetailOffset: off,
				Message:      "control byte in header value",
			}
		}
		if b == 0x7f {
			return Header{}, &ParseError{
				Kind:         ErrHeaderLine,
				Offset:       off,
				DetailOffset: off,
				Message:      "DEL byte in header value",
			}
		}
	}
	return Header{Name: string(name), Value: string(value), Offset: off}, nil
}

func parseHeaderBlock(raw []byte, start int) (headers []Header, end int, perr *ParseError) {
	pos := start
	for {
		line, next, ok := readLine(raw, pos)
		if !ok {
			return headers, len(raw), &ParseError{
				Kind:         ErrHeaderEOF,
				Offset:       len(raw),
				DetailOffset: pos,
				Message:      "header block is not terminated",
			}
		}
		if len(line) == 0 {
			return headers, next, nil
		}
		h, err := parseHeaderLine(line, pos)
		if err != nil {
			return headers, pos, err
		}
		headers = append(headers, h)
		pos = next
	}
}

func isDecimal(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isHex(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func isToken(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '!' || c == '#' || c == '$' || c == '%' || c == '&' || c == '\'' ||
			c == '*' || c == '+' || c == '-' || c == '.' || c == '^' || c == '_' ||
			c == '`' || c == '|' || c == '~':
		default:
			return false
		}
	}
	return true
}

type contentLengthInfo struct {
	Present      bool
	Value        int64
	Repeated     bool
	Conflicting  bool
	Invalid      bool
	Error        *ParseError
	DetailOffset int
}

func analyzeContentLength(headers []Header, headerEnd int) contentLengthInfo {
	info := contentLengthInfo{Value: -1, DetailOffset: -1}
	var values []int64
	for _, h := range headers {
		if !strings.EqualFold(h.Name, "Content-Length") {
			continue
		}
		if !info.Present {
			info.Present = true
			info.DetailOffset = h.Offset
		} else {
			info.Repeated = true
		}
		parts := strings.Split(h.Value, ",")
		if len(parts) > 1 {
			info.Repeated = true
			if info.DetailOffset < 0 {
				info.DetailOffset = h.Offset
			}
		}
		for _, part := range parts {
			token := strings.Trim(part, " \t")
			if token == "" || !isDecimal([]byte(token)) {
				info.Invalid = true
				info.Error = &ParseError{
					Kind:          ErrInvalidContentLength,
					Offset:        headerEnd,
					DetailOffset:  h.Offset,
					Message:       fmt.Sprintf("invalid Content-Length value %q", token),
					ResponseIndex: -1,
				}
				return info
			}
			n, err := strconv.ParseUint(token, 10, 63)
			if err != nil || n > uint64(maxInt) {
				info.Invalid = true
				info.Error = &ParseError{
					Kind:          ErrInvalidContentLength,
					Offset:        headerEnd,
					DetailOffset:  h.Offset,
					Message:       fmt.Sprintf("Content-Length %q overflows", token),
					ResponseIndex: -1,
				}
				return info
			}
			value := int64(n)
			if len(values) > 0 && value != values[0] {
				info.Conflicting = true
			}
			if len(values) > 0 {
				info.Repeated = true
			}
			values = append(values, value)
		}
	}
	if len(values) > 0 {
		info.Value = values[0]
	}
	if info.Conflicting {
		info.Error = &ParseError{
			Kind:          ErrConflictingContentLength,
			Offset:        headerEnd,
			DetailOffset:  info.DetailOffset,
			Message:       "Content-Length values disagree",
			ResponseIndex: -1,
		}
	}
	return info
}

type transferEncodingInfo struct {
	Present      bool
	Chunked      bool
	Tokens       []string
	DetailOffset int
}

func analyzeTransferEncoding(headers []Header) transferEncodingInfo {
	info := transferEncodingInfo{DetailOffset: -1}
	for _, h := range headers {
		if !strings.EqualFold(h.Name, "Transfer-Encoding") {
			continue
		}
		info.Present = true
		if info.DetailOffset < 0 {
			info.DetailOffset = h.Offset
		}
		for _, part := range strings.Split(h.Value, ",") {
			token := strings.ToLower(strings.Trim(part, " \t"))
			if token == "" {
				continue
			}
			info.Tokens = append(info.Tokens, token)
			if token == "chunked" {
				info.Chunked = true
			}
		}
	}
	return info
}

func parseChunkedBody(raw []byte, bodyStart int) (end int, trailers []Header, truncated bool, perr *ParseError) {
	pos := bodyStart
	for {
		line, next, ok := readLine(raw, pos)
		if !ok {
			return len(raw), trailers, true, nil
		}
		sizePart := line
		if semi := bytes.IndexByte(line, ';'); semi >= 0 {
			sizePart = line[:semi]
		}
		sizePart = bytes.TrimSpace(sizePart)
		if !isHex(sizePart) {
			return pos, trailers, false, &ParseError{
				Kind:         ErrChunked,
				Offset:       pos,
				DetailOffset: pos,
				Message:      "invalid chunk size",
			}
		}
		n, err := strconv.ParseUint(string(sizePart), 16, 63)
		if err != nil || n > uint64(maxInt) {
			return pos, trailers, false, &ParseError{
				Kind:         ErrChunked,
				Offset:       pos,
				DetailOffset: pos,
				Message:      "chunk size overflows",
			}
		}
		pos = next
		if n == 0 {
			for {
				tline, tnext, ok := readLine(raw, pos)
				if !ok {
					return len(raw), trailers, true, nil
				}
				if len(tline) == 0 {
					return tnext, trailers, false, nil
				}
				h, herr := parseHeaderLine(tline, pos)
				if herr != nil {
					return pos, trailers, false, herr
				}
				trailers = append(trailers, h)
				pos = tnext
			}
		}
		if int(n) > len(raw)-pos {
			return len(raw), trailers, true, nil
		}
		pos += int(n)
		if pos >= len(raw) {
			return len(raw), trailers, true, nil
		}
		switch raw[pos] {
		case '\r':
			if pos+1 >= len(raw) {
				return len(raw), trailers, true, nil
			}
			if raw[pos+1] != '\n' {
				return pos, trailers, false, &ParseError{
					Kind:         ErrChunked,
					Offset:       pos,
					DetailOffset: pos,
					Message:      "chunk data is not followed by CRLF",
				}
			}
			pos += 2
		case '\n':
			pos++
		default:
			return pos, trailers, false, &ParseError{
				Kind:         ErrChunked,
				Offset:       pos,
				DetailOffset: pos,
				Message:      "chunk data is not followed by a newline",
			}
		}
	}
}

func finding(strategy string, kind FindingKind, respIndex, reqIndex, offset int, message string) Finding {
	return Finding{
		Kind:          kind,
		Strategy:      strategy,
		ResponseIndex: respIndex,
		RequestIndex:  reqIndex,
		Offset:        offset,
		Message:       message,
	}
}

func parseOne(raw []byte, start, index, requestIndex int, method, strategy string, lenient bool) (*Response, *ParseError, []Finding) {
	protocol, code, reason, headerStart, serr := parseStatusLine(raw, start)
	if serr != nil {
		serr.ResponseIndex = index
		serr.RequestIndex = requestIndex
		return nil, serr, nil
	}
	headers, headerEnd, herr := parseHeaderBlock(raw, headerStart)
	if herr != nil {
		herr.ResponseIndex = index
		herr.RequestIndex = requestIndex
		informational := code >= 100 && code <= 199
		partial := &Response{
			Index:          index,
			RequestIndex:   requestIndex,
			StatusCode:     code,
			Protocol:       protocol,
			Reason:         reason,
			Headers:        headers,
			Start:          start,
			HeaderEnd:      herr.Offset,
			BodyStart:      herr.Offset,
			BodyEnd:        herr.Offset,
			End:            herr.Offset,
			Boundary:       BoundaryInvalid,
			DeclaredLength: -1,
			ExpectedEnd:    -1,
			Informational:  informational,
			NoBody:         informational || code == 204 || isHeadMethod(method),
		}
		partial.Raw = raw[start:herr.Offset]
		partial.Error = herr.Error()
		return partial, herr, nil
	}

	cl := analyzeContentLength(headers, headerEnd)
	te := analyzeTransferEncoding(headers)
	informational := code >= 100 && code <= 199
	head := isHeadMethod(method)
	noBody := informational || head || code == 204

	resp := &Response{
		Index:          index,
		RequestIndex:   requestIndex,
		StatusCode:     code,
		Protocol:       protocol,
		Reason:         reason,
		Headers:        headers,
		Start:          start,
		HeaderEnd:      headerEnd,
		BodyStart:      headerEnd,
		BodyEnd:        headerEnd,
		End:            headerEnd,
		Boundary:       BoundaryNone,
		DeclaredLength: -1,
		ExpectedEnd:    -1,
		Informational:  informational,
		NoBody:         noBody,
	}

	var findings []Finding
	if cl.Present && !cl.Invalid {
		resp.DeclaredLength = cl.Value
	}
	if cl.Repeated {
		findings = append(findings, finding(strategy, FindingRepeatedLength, index, requestIndex, cl.DetailOffset, "repeated Content-Length"))
	}
	if cl.Conflicting {
		findings = append(findings, finding(strategy, FindingRepeatedLength, index, requestIndex, cl.DetailOffset, "conflicting Content-Length"))
	}
	if te.Present {
		if te.Chunked {
			findings = append(findings, finding(strategy, FindingChunked, index, requestIndex, te.DetailOffset, "chunked transfer coding"))
		}
		if cl.Present {
			findings = append(findings, finding(strategy, FindingConflictingFraming, index, requestIndex, headerEnd, "Content-Length and Transfer-Encoding are both present"))
		}
	}
	if informational {
		findings = append(findings, finding(strategy, FindingInformational, index, requestIndex, start, "informational response does not consume a request"))
	}
	if head && !informational {
		findings = append(findings, finding(strategy, FindingHeadNoBody, index, requestIndex, start, "HEAD response has no body"))
	}
	if code == 204 && !informational {
		findings = append(findings, finding(strategy, FindingNoContentNoBody, index, requestIndex, start, "204 response has no body"))
	}

	if noBody {
		resp.DeclaredLength = firstDeclaredLength(cl)
		resp.Boundary = BoundaryNone
		resp.BodyStart = headerEnd
		resp.BodyEnd = headerEnd
		resp.End = headerEnd
		resp.Body = raw[headerEnd:headerEnd]
		resp.Raw = raw[start:headerEnd]
		return resp, nil, findings
	}

	if te.Present {
		if lenient && te.Chunked {
			end, trailers, truncated, cerr := parseChunkedBody(raw, headerEnd)
			resp.Boundary = BoundaryChunked
			resp.Chunked = true
			resp.Trailers = trailers
			resp.BodyStart = headerEnd
			resp.BodyEnd = end
			resp.End = end
			resp.Truncated = truncated
			resp.Body = raw[headerEnd:end]
			resp.Raw = raw[start:end]
			if cerr != nil {
				cerr.ResponseIndex = index
				cerr.RequestIndex = requestIndex
				resp.Error = cerr.Error()
				return resp, cerr, findings
			}
			if truncated {
				findings = append(findings, finding(strategy, FindingEarlyClose, index, requestIndex, end, "chunked body ended before its terminator"))
			}
			return resp, nil, findings
		}
		kind := ErrTransferEncoding
		message := "Transfer-Encoding is not allowed by the boundary policy"
		if cl.Present {
			kind = ErrConflictingFraming
			message = "Content-Length and Transfer-Encoding conflict"
		} else if te.Chunked {
			kind = ErrChunked
			message = "chunked transfer coding is not allowed by the boundary policy"
		}
		resp.Boundary = BoundaryInvalid
		resp.BodyStart = headerEnd
		resp.BodyEnd = headerEnd
		resp.End = headerEnd
		resp.Body = raw[headerEnd:headerEnd]
		resp.Raw = raw[start:headerEnd]
		perr := &ParseError{
			Kind:          kind,
			Offset:        headerEnd,
			DetailOffset:  te.DetailOffset,
			Message:       message,
			ResponseIndex: index,
			RequestIndex:  requestIndex,
		}
		resp.Error = perr.Error()
		return resp, perr, findings
	}

	if cl.Invalid {
		resp.Boundary = BoundaryInvalid
		resp.BodyStart = headerEnd
		resp.BodyEnd = headerEnd
		resp.End = headerEnd
		resp.Body = raw[headerEnd:headerEnd]
		resp.Raw = raw[start:headerEnd]
		cl.Error.ResponseIndex = index
		cl.Error.RequestIndex = requestIndex
		resp.Error = cl.Error.Error()
		return resp, cl.Error, findings
	}
	if !lenient && (cl.Conflicting || cl.Repeated) {
		resp.Boundary = BoundaryInvalid
		resp.BodyStart = headerEnd
		resp.BodyEnd = headerEnd
		resp.End = headerEnd
		resp.Body = raw[headerEnd:headerEnd]
		resp.Raw = raw[start:headerEnd]
		kind := ErrRepeatedContentLength
		message := "Content-Length is repeated"
		if cl.Conflicting {
			kind = ErrConflictingContentLength
			message = "Content-Length values disagree"
		}
		perr := &ParseError{
			Kind:          kind,
			Offset:        headerEnd,
			DetailOffset:  cl.DetailOffset,
			Message:       message,
			ResponseIndex: index,
			RequestIndex:  requestIndex,
		}
		resp.Error = perr.Error()
		return resp, perr, findings
	}

	if cl.Present {
		length := cl.Value
		if length > int64(maxInt-headerEnd) {
			resp.Boundary = BoundaryInvalid
			resp.BodyStart = headerEnd
			resp.BodyEnd = headerEnd
			resp.End = headerEnd
			resp.Body = raw[headerEnd:headerEnd]
			resp.Raw = raw[start:headerEnd]
			perr := &ParseError{
				Kind:          ErrInvalidContentLength,
				Offset:        headerEnd,
				DetailOffset:  cl.DetailOffset,
				Message:       "Content-Length is too large for the raw stream",
				ResponseIndex: index,
				RequestIndex:  requestIndex,
			}
			resp.Error = perr.Error()
			return resp, perr, findings
		}
		expected := headerEnd + int(length)
		resp.Boundary = BoundaryContentLength
		resp.DeclaredLength = length
		resp.ExpectedEnd = expected
		resp.BodyStart = headerEnd
		if expected > len(raw) {
			resp.Truncated = true
			resp.BodyEnd = len(raw)
			resp.End = len(raw)
			findings = append(findings, finding(strategy, FindingEarlyClose, index, requestIndex, len(raw), "connection closed before Content-Length was satisfied"))
			if !lenient {
				perr := &ParseError{
					Kind:          ErrEarlyClose,
					Offset:        len(raw),
					DetailOffset:  expected,
					Message:       fmt.Sprintf("connection closed at byte %d; Content-Length requires byte %d", len(raw), expected),
					ResponseIndex: index,
					RequestIndex:  requestIndex,
				}
				resp.Body = raw[headerEnd:len(raw)]
				resp.Raw = raw[start:len(raw)]
				resp.Error = perr.Error()
				return resp, perr, findings
			}
		} else {
			resp.BodyEnd = expected
			resp.End = expected
		}
		resp.Body = raw[resp.BodyStart:resp.BodyEnd]
		resp.Raw = raw[start:resp.End]
		return resp, nil, findings
	}

	resp.Boundary = BoundaryClose
	resp.BodyStart = headerEnd
	resp.BodyEnd = len(raw)
	resp.End = len(raw)
	resp.Body = raw[headerEnd:len(raw)]
	resp.Raw = raw[start:len(raw)]
	findings = append(findings, finding(strategy, FindingCloseDelimited, index, requestIndex, headerEnd, "body is delimited by connection close"))
	return resp, nil, findings
}

func firstDeclaredLength(cl contentLengthInfo) int64 {
	if cl.Present && !cl.Invalid {
		return cl.Value
	}
	return -1
}

func parseResponses(methods []string, raw []byte, strategy string, lenient bool) ParseResult {
	result := ParseResult{
		Strategy: strategy,
		Methods:  append([]string(nil), methods...),
		Raw:      raw,
	}
	requestIndex := 0
	pos := 0
	responseIndex := 0
	for pos < len(raw) {
		method := methodAt(methods, requestIndex)
		resp, perr, found := parseOne(raw, pos, responseIndex, requestIndex, method, strategy, lenient)
		for _, f := range found {
			result.Findings = appendUniqueFinding(result.Findings, f)
		}
		if resp != nil {
			result.Responses = append(result.Responses, *resp)
			if resp.IsFinal() {
				requestIndex++
			}
			responseIndex++
		}
		if perr != nil {
			result.Err = perr
			break
		}
		if resp == nil {
			result.Err = &ParseError{
				Kind:          ErrNoProgress,
				Offset:        pos,
				DetailOffset:  pos,
				Message:       "parser produced neither a response nor an error",
				ResponseIndex: responseIndex,
				RequestIndex:  requestIndex,
			}
			break
		}
		if resp.End <= pos {
			result.Err = &ParseError{
				Kind:          ErrNoProgress,
				Offset:        pos,
				DetailOffset:  pos,
				Message:       "response did not advance the stream",
				ResponseIndex: resp.Index,
				RequestIndex:  resp.RequestIndex,
			}
			break
		}
		pos = resp.End
	}
	finishResult(&result)
	return result
}

func appendUniqueFinding(findings []Finding, f Finding) []Finding {
	for _, existing := range findings {
		if existing.Kind == f.Kind && existing.Strategy == f.Strategy &&
			existing.ResponseIndex == f.ResponseIndex && existing.RequestIndex == f.RequestIndex {
			return findings
		}
	}
	return append(findings, f)
}

func finishResult(result *ParseResult) {
	for i := range result.Findings {
		if result.Findings[i].Strategy == "" {
			result.Findings[i].Strategy = result.Strategy
		}
	}
	for _, resp := range result.Responses {
		if resp.RequestIndex < 0 || resp.RequestIndex >= len(result.Methods) {
			result.Findings = appendUniqueFinding(result.Findings, finding(
				result.Strategy,
				FindingUnattributedResponse,
				resp.Index,
				resp.RequestIndex,
				resp.Start,
				fmt.Sprintf("response %d has no request in the method queue", resp.Index),
			))
		}
	}
	answered := make([]bool, len(result.Methods))
	for _, resp := range result.Responses {
		if resp.IsFinal() && resp.RequestIndex >= 0 && resp.RequestIndex < len(result.Methods) {
			answered[resp.RequestIndex] = true
		}
	}
	for i, ok := range answered {
		if !ok {
			result.Findings = appendUniqueFinding(result.Findings, finding(
				result.Strategy,
				FindingUnansweredRequest,
				-1,
				i,
				len(result.Raw),
				fmt.Sprintf("method queue entry %d has no final response", i),
			))
		}
	}
}
