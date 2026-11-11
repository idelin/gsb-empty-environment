package mimeingest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/textproto"
	"strings"
)

// budget tracks the shared decoded-byte allowance of one message.
type budget struct {
	limit int64
	used  int64
}

func (b *budget) charge(n int64) error {
	b.used += n
	if b.used > b.limit {
		return ErrBudgetExceeded
	}
	return nil
}

// boundary is a delimiter marker: "--" + the boundary parameter.
type boundary struct {
	marker []byte
}

type walker struct {
	ctx    context.Context
	br     *bufio.Reader
	cfg    *Config
	budget *budget
	staged []Object
	result *Result
	parts  int
}

// readLine returns one line including its line ending. A returned error
// of io.EOF means the final line has no ending.
func (w *walker) readLine() ([]byte, error) {
	var line []byte
	for {
		frag, err := w.br.ReadSlice('\n')
		line = append(line, frag...)
		if err == bufio.ErrBufferFull {
			if len(line) > maxLineLength {
				return nil, errors.New("mimeingest: line too long")
			}
			continue
		}
		return line, err
	}
}

func splitEnding(line []byte) (content, ending []byte) {
	if n := len(line); n >= 2 && line[n-2] == '\r' && line[n-1] == '\n' {
		return line[:n-2], line[n-2:]
	}
	if n := len(line); n >= 1 && line[n-1] == '\n' {
		return line[:n-1], line[n-1:]
	}
	return line, nil
}

func isTransportPadding(b []byte) bool {
	for _, c := range b {
		if c != ' ' && c != '\t' {
			return false
		}
	}
	return true
}

// matchBoundary reports whether content (a line without its ending) is a
// delimiter for one of bounds, which are checked innermost first.
func matchBoundary(content []byte, bounds []boundary) (idx int, closing, ok bool) {
	for i, b := range bounds {
		rest, found := bytes.CutPrefix(content, b.marker)
		if !found {
			continue
		}
		c := false
		if r, f := bytes.CutPrefix(rest, []byte("--")); f {
			c = true
			rest = r
		}
		if isTransportPadding(rest) {
			return i, c, true
		}
	}
	return 0, false, false
}

// scanBody copies raw (still transfer-encoded) body bytes to dst until a
// line matching one of bounds is seen. The CRLF immediately preceding
// the boundary belongs to the delimiter and is not copied. It returns
// the matched boundary.
func (w *walker) scanBody(dst io.Writer, bounds []boundary) (int, bool, error) {
	var pending []byte
	for {
		line, err := w.readLine()
		if err != nil && err != io.EOF {
			return 0, false, err
		}
		content, ending := splitEnding(line)
		if idx, closing, ok := matchBoundary(content, bounds); ok {
			return idx, closing, nil
		}
		if len(pending) > 0 {
			if _, werr := dst.Write(pending); werr != nil {
				return 0, false, werr
			}
		}
		if len(content) > 0 {
			if _, werr := dst.Write(content); werr != nil {
				return 0, false, werr
			}
		}
		pending = ending
		if err == io.EOF {
			return 0, false, ErrUnterminated
		}
	}
}

// walkMultipart consumes a multipart body: preamble, parts, closing
// delimiter and epilogue. With ancestors present it returns the
// enclosing boundary that terminated this part; at the top level it
// verifies that only whitespace follows the final boundary.
func (w *walker) walkMultipart(param string, ancestors []boundary, depth int) (int, bool, error) {
	if depth > w.cfg.MaxDepth {
		return 0, false, errors.New("mimeingest: multipart nesting too deep")
	}
	own := boundary{marker: []byte("--" + param)}
	bounds := append([]boundary{own}, ancestors...)

	// Preamble: discard everything up to the first delimiter.
	idx, closing, err := w.scanBody(io.Discard, bounds)
	if err != nil {
		return 0, false, err
	}
	if idx != 0 {
		return 0, false, fmt.Errorf("mimeingest: multipart %q has no parts", param)
	}
	if closing {
		return 0, false, fmt.Errorf("mimeingest: multipart %q is empty", param)
	}

	for {
		hdr, err := readHeader(w.br)
		if err != nil {
			return 0, false, fmt.Errorf("mimeingest: reading part headers: %w", err)
		}
		idx, closing, err := w.walkPart(hdr, bounds, depth)
		if err != nil {
			return 0, false, err
		}
		if idx != 0 {
			return 0, false, fmt.Errorf("mimeingest: multipart %q missing closing boundary", param)
		}
		if closing {
			break
		}
	}

	// Epilogue.
	if len(ancestors) == 0 {
		if err := w.checkTrailing(); err != nil {
			return 0, false, err
		}
		return 0, false, nil
	}
	return w.scanBody(io.Discard, ancestors)
}

func (w *walker) walkPart(hdr textproto.MIMEHeader, bounds []boundary, depth int) (int, bool, error) {
	w.parts++
	if w.parts > maxParts {
		return 0, false, errors.New("mimeingest: too many parts")
	}
	mediaType, params, err := contentType(hdr)
	if err != nil {
		return 0, false, err
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		b := params["boundary"]
		if b == "" {
			return 0, false, fmt.Errorf("mimeingest: %s part missing boundary parameter", mediaType)
		}
		return w.walkMultipart(b, bounds, depth+1)
	}
	return w.walkLeaf(hdr, mediaType, params, bounds)
}

// walkLeaf decodes one leaf part body into its sink, stopping at an
// enclosing boundary.
func (w *walker) walkLeaf(hdr textproto.MIMEHeader, mediaType string, ctParams map[string]string, bounds []boundary) (int, bool, error) {
	sink, err := w.openSink(hdr, mediaType, ctParams)
	if err != nil {
		return 0, false, err
	}
	enc := strings.ToLower(strings.TrimSpace(hdr.Get("Content-Transfer-Encoding")))
	pr, pw := io.Pipe()
	dec, err := decoderFor(enc, pr)
	if err != nil {
		return 0, false, err
	}
	errc := make(chan error, 1)
	go func() {
		_, cerr := io.Copy(sink.cw, dec)
		pr.CloseWithError(cerr)
		errc <- cerr
	}()
	idx, closing, serr := w.scanBody(pw, bounds)
	if serr != nil {
		pw.CloseWithError(serr)
	} else {
		pw.Close()
	}
	derr := <-errc
	if serr != nil {
		return 0, false, fmt.Errorf("mimeingest: reading part body: %w", serr)
	}
	if derr != nil {
		return 0, false, fmt.Errorf("mimeingest: decoding part body: %w", derr)
	}
	if err := w.closeSink(sink); err != nil {
		return 0, false, err
	}
	return idx, closing, nil
}

// walkRootLeaf handles a non-multipart message whose body runs to EOF.
func (w *walker) walkRootLeaf(hdr textproto.MIMEHeader, mediaType string, ctParams map[string]string) error {
	sink, err := w.openSink(hdr, mediaType, ctParams)
	if err != nil {
		return err
	}
	enc := strings.ToLower(strings.TrimSpace(hdr.Get("Content-Transfer-Encoding")))
	dec, err := decoderFor(enc, w.br)
	if err != nil {
		return err
	}
	if _, err := io.Copy(sink.cw, dec); err != nil {
		return fmt.Errorf("mimeingest: decoding message body: %w", err)
	}
	return w.closeSink(sink)
}

// checkTrailing verifies that only whitespace follows the final boundary
// of the top-level multipart.
func (w *walker) checkTrailing() error {
	for {
		line, err := w.readLine()
		content, _ := splitEnding(line)
		if !isTransportPadding(content) {
			return ErrTrailingData
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// leafSink couples the staged object and scan session of one part.
type leafSink struct {
	cw    *chargeWriter
	obj   Object
	sess  Session
	meta  Attachment
	isAtt bool
}

func (w *walker) openSink(hdr textproto.MIMEHeader, mediaType string, ctParams map[string]string) (*leafSink, error) {
	s := &leafSink{}
	s.meta, s.isAtt = attachmentMeta(hdr, mediaType, ctParams)
	dsts := []io.Writer{io.Discard}
	if s.isAtt {
		obj, err := w.cfg.Store.Stage(w.ctx, s.meta)
		if err != nil {
			return nil, fmt.Errorf("mimeingest: staging %q: %w", s.meta.Filename, err)
		}
		// Track the object immediately so a later failure withdraws it.
		w.staged = append(w.staged, obj)
		s.obj = obj
		dsts = append(dsts, obj)
		if w.cfg.Scanner != nil {
			sess, err := w.cfg.Scanner.Begin(s.meta)
			if err != nil {
				return nil, fmt.Errorf("mimeingest: scanner begin: %w", err)
			}
			s.sess = sess
			dsts = append(dsts, sess)
		}
	}
	s.cw = &chargeWriter{budget: w.budget, dst: io.MultiWriter(dsts...)}
	return s, nil
}

func (w *walker) closeSink(s *leafSink) error {
	if !s.isAtt {
		return nil
	}
	s.meta.Size = s.cw.n
	if s.sess != nil {
		if verr := s.sess.Verdict(); verr != nil {
			return fmt.Errorf("%w: %q", ErrInfected, s.meta.Filename)
		}
	}
	w.result.Attachments = append(w.result.Attachments, s.meta)
	return nil
}

// chargeWriter charges decoded bytes against the shared budget before
// forwarding them.
type chargeWriter struct {
	budget *budget
	dst    io.Writer
	n      int64
}

func (c *chargeWriter) Write(p []byte) (int, error) {
	if err := c.budget.charge(int64(len(p))); err != nil {
		return 0, err
	}
	n, err := c.dst.Write(p)
	c.n += int64(n)
	return n, err
}

func decoderFor(enc string, r io.Reader) (io.Reader, error) {
	switch enc {
	case "", "7bit", "8bit", "binary":
		return r, nil
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, r), nil
	case "quoted-printable":
		return quotedprintable.NewReader(r), nil
	default:
		return nil, fmt.Errorf("mimeingest: unsupported Content-Transfer-Encoding %q", enc)
	}
}

func contentType(hdr textproto.MIMEHeader) (string, map[string]string, error) {
	ct := hdr.Get("Content-Type")
	if ct == "" {
		return "text/plain", nil, nil
	}
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return "", nil, fmt.Errorf("mimeingest: invalid Content-Type %q: %w", ct, err)
	}
	return strings.ToLower(mt), params, nil
}

func attachmentMeta(hdr textproto.MIMEHeader, mediaType string, ctParams map[string]string) (Attachment, bool) {
	meta := Attachment{ContentType: mediaType}
	dispType := ""
	var dispParams map[string]string
	if disp := hdr.Get("Content-Disposition"); disp != "" {
		if d, p, err := mime.ParseMediaType(disp); err == nil {
			dispType, dispParams = strings.ToLower(d), p
		}
	}
	meta.Filename = dispParams["filename"]
	if meta.Filename == "" {
		meta.Filename = ctParams["name"]
	}
	return meta, dispType == "attachment" || meta.Filename != ""
}

func readHeader(br *bufio.Reader) (textproto.MIMEHeader, error) {
	tp := textproto.NewReader(br)
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		return nil, err
	}
	return hdr, nil
}
