package warcwriter

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MIME types used for WARC record blocks.
const (
	mimeHTTPRequest  = "application/http;msgtype=request"
	mimeHTTPResponse = "application/http;msgtype=response"
	mimeWarcFields   = "application/warc-fields"

	defaultMaxFileBytes = int64(100 * 1024 * 1024)
)

// Options controls Writer behavior.
type Options struct {
	// Dir receives rolled WARC (.warc.gz) and CDXJ (.cdxj) files.
	Dir string
	// Prefix is the leading component of generated file names.
	Prefix string
	// MaxFileBytes rolls the WARC file when the next capture would make it
	// exceed this size. Zero selects the default (100 MiB).
	MaxFileBytes int64
	// CDXJPath overrides the index location. Defaults to
	// <Dir>/<Prefix>.cdxj.
	CDXJPath string
	// Now supplies timestamps; defaults to time.Now.
	Now func() time.Time
}

// Capture is one archived URL exchange.
type Capture struct {
	// Request is the raw HTTP request as sent on the wire.
	Request []byte
	// Response is the raw HTTP response received on the wire.
	Response []byte
	// Metadata becomes the application/warc-fields metadata record body.
	Metadata map[string]string
}

// CaptureResult reports the records written for one Capture.
type CaptureResult struct {
	RequestID   string
	ResponseID  string
	MetadataID  string
	IsRevisit   bool
	PayloadKey  string
	WARCFile    string
	IndexOffset int64
	IndexLength int64
}

// Writer archives captures into size-rolled, independently gzipped WARC/1.1
// files and maintains a CDXJ index. It is safe for concurrent use.
type Writer struct {
	opts Options
	now  func() time.Time

	mu          sync.Mutex
	seq         int
	file        *os.File
	currentName string
	currentSize int64
	cdxj        *os.File
	closed      bool

	// seen maps a payload digest to the original record's descriptor.
	seen map[string]*payloadOrigin
}

type payloadOrigin struct {
	recordID  string
	targetURI string
	warcFile  string
	date      time.Time
}

// NewWriter opens (or creates) the archive directory and index file.
func NewWriter(opts Options) (*Writer, error) {
	if opts.Dir == "" {
		return nil, errors.New("warcwriter: Options.Dir is required")
	}
	if opts.Prefix == "" {
		opts.Prefix = "crawl"
	}
	if opts.MaxFileBytes <= 0 {
		opts.MaxFileBytes = defaultMaxFileBytes
	}
	if opts.CDXJPath == "" {
		opts.CDXJPath = filepath.Join(opts.Dir, opts.Prefix+".cdxj")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("warcwriter: create dir: %w", err)
	}

	w := &Writer{
		opts: opts,
		now:  now,
		seen: make(map[string]*payloadOrigin),
	}
	if err := w.loadExistingIndex(); err != nil {
		return nil, err
	}
	cdxj, err := os.OpenFile(opts.CDXJPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("warcwriter: open cdxj: %w", err)
	}
	w.cdxj = cdxj
	return w, nil
}

// loadExistingIndex hydrates the dedup table and sequence number so that a
// reopened Writer still rewrites payloads archived in earlier files.
func (w *Writer) loadExistingIndex() error {
	f, err := os.Open(w.opts.CDXJPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("warcwriter: read cdxj: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "{") {
			continue
		}
		entry, err := parseCDXJLine(line)
		if err != nil {
			continue
		}
		w.advanceSequence(entry.Filename)
		if entry.Revisit || entry.PayloadDigest == "" {
			continue
		}
		w.seen[entry.PayloadDigest] = &payloadOrigin{
			recordID:  entry.ID,
			targetURI: entry.URI,
			warcFile:  entry.Filename,
			date:      entry.Date,
		}
	}
	return scanner.Err()
}

func (w *Writer) advanceSequence(name string) {
	base := filepath.Base(name)
	prefix := w.opts.Prefix + "-"
	if !strings.HasPrefix(base, prefix) {
		return
	}
	rest := strings.TrimPrefix(base, prefix)
	rest = strings.SplitN(rest, "-", 2)[0]
	var n int
	if _, err := fmt.Sscanf(rest, "%05d", &n); err == nil && n >= w.seq {
		w.seq = n
	}
}

// WriteCapture archives one request/response exchange.
func (w *Writer) WriteCapture(cap Capture) (*CaptureResult, error) {
	if len(cap.Request) == 0 {
		return nil, errors.New("warcwriter: capture request is empty")
	}
	if len(cap.Response) == 0 {
		return nil, errors.New("warcwriter: capture response is empty")
	}

	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(cap.Request)))
	if err != nil {
		return nil, fmt.Errorf("warcwriter: parse request: %w", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(cap.Response)), req)
	if err != nil {
		return nil, fmt.Errorf("warcwriter: parse response: %w", err)
	}

	reqEntity, err := httpEntityBody(cap.Request, true)
	if err != nil {
		return nil, err
	}
	respEntity, err := httpEntityBody(cap.Response, false)
	if err != nil {
		return nil, err
	}
	respHead, _ := splitMessage(cap.Response)

	targetURI := requestTargetURI(req)
	date := w.now().UTC()
	reqID := newRecordID()
	respID := newRecordID()
	metaID := newRecordID()

	payloadKey := LabeledSHA1(respEntity)

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, errors.New("warcwriter: writer is closed")
	}

	if err := w.ensureFile(date); err != nil {
		return nil, err
	}

	result := &CaptureResult{
		RequestID:  reqID,
		ResponseID: respID,
		MetadataID: metaID,
		PayloadKey: payloadKey,
		WARCFile:   w.currentName,
	}

	// 1) request record.
	reqRecord := &Record{
		Type:          TypeRequest,
		TargetURI:     targetURI,
		Date:          date,
		RecordID:      reqID,
		ContentType:   mimeHTTPRequest,
		PayloadDigest: LabeledSHA1(reqEntity),
		Body:          cap.Request,
	}
	if _, _, err := w.writeRecord(reqRecord); err != nil {
		return nil, err
	}

	origin, duplicate := w.seen[payloadKey]
	result.IsRevisit = duplicate

	// 2) response or revisit record; capture its gzip member bounds.
	memberOffset := w.currentSize
	var indexEntry *cdxjEntry
	if duplicate {
		extra := map[string]string{
			"WARC-Refers-To-Target-URI": origin.targetURI,
		}
		if !origin.date.IsZero() {
			extra["WARC-Refers-To-Date"] = origin.date.UTC().Format(time.RFC3339Nano)
		}
		revisitRecord := &Record{
			Type:          TypeRevisit,
			TargetURI:     targetURI,
			Date:          date,
			RecordID:      respID,
			ConcurrentTo:  reqID,
			RefersTo:      origin.recordID,
			Profile:       RevisitProfileIdenticalPayload,
			ContentType:   mimeHTTPResponse,
			PayloadDigest: payloadKey,
			Extra:         extra,
			Body:          respHead,
		}
		if _, _, err := w.writeRecord(revisitRecord); err != nil {
			return nil, err
		}
		indexEntry = &cdxjEntry{
			SURT:          surt(targetURI),
			Timestamp:     cdxjTimestamp(date),
			Date:          date,
			URI:           targetURI,
			Status:        fmt.Sprintf("%d", resp.StatusCode),
			MIME:          contentTypeOf(resp),
			Filename:      w.currentName,
			Offset:        memberOffset,
			Digest:        payloadKey,
			PayloadDigest: payloadKey,
			ID:            respID,
			Revisit:       true,
			RefersTo:      origin.recordID,
		}
	} else {
		respRecord := &Record{
			Type:          TypeResponse,
			TargetURI:     targetURI,
			Date:          date,
			RecordID:      respID,
			ConcurrentTo:  reqID,
			ContentType:   mimeHTTPResponse,
			PayloadDigest: payloadKey,
			Body:          cap.Response,
		}
		if _, _, err := w.writeRecord(respRecord); err != nil {
			return nil, err
		}
		indexEntry = &cdxjEntry{
			SURT:          surt(targetURI),
			Timestamp:     cdxjTimestamp(date),
			Date:          date,
			URI:           targetURI,
			Status:        fmt.Sprintf("%d", resp.StatusCode),
			MIME:          contentTypeOf(resp),
			Filename:      w.currentName,
			Offset:        memberOffset,
			Digest:        payloadKey,
			PayloadDigest: payloadKey,
			ID:            respID,
		}
	}
	memberLength := w.currentSize - memberOffset
	indexEntry.Length = memberLength
	result.IndexOffset = memberOffset
	result.IndexLength = memberLength

	// 3) metadata record, concurrent with the request.
	metaRecord := &Record{
		Type:         TypeMetadata,
		TargetURI:    targetURI,
		Date:         date,
		RecordID:     metaID,
		ConcurrentTo: reqID,
		ContentType:  mimeWarcFields,
		Body:         warcFieldsBody(cap.Metadata),
	}
	if _, _, err := w.writeRecord(metaRecord); err != nil {
		return nil, err
	}

	if err := w.appendIndex(indexEntry); err != nil {
		return nil, err
	}

	if !duplicate {
		w.seen[payloadKey] = &payloadOrigin{
			recordID:  respID,
			targetURI: targetURI,
			warcFile:  w.currentName,
			date:      date,
		}
	}
	return result, nil
}

// ensureFile opens the first WARC file or rolls when the limit is exceeded.
func (w *Writer) ensureFile(date time.Time) error {
	if w.file != nil && w.currentSize <= w.opts.MaxFileBytes {
		return nil
	}
	if err := w.roll(date); err != nil {
		return err
	}
	info := &Record{
		Type:        TypeWarcinfo,
		Date:        date,
		RecordID:    newRecordID(),
		ContentType: mimeWarcFields,
		Body: warcFieldsBody(map[string]string{
			"software":   "warcwriter (Go standard library)",
			"format":     "WARC File Format 1.1",
			"conformsTo": "http://bibnum.bnf.fr/WARC/WARC_ISO_28500_version1_latestdraft.pdf",
			"publisher":  w.opts.Prefix,
			"isPartOf":   w.opts.Prefix,
		}),
	}
	_, _, err := w.writeRecord(info)
	return err
}

func (w *Writer) roll(date time.Time) error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return fmt.Errorf("warcwriter: close warc: %w", err)
		}
	}
	w.seq++
	name := filepath.Join(w.opts.Dir, fmt.Sprintf("%s-%05d-%s.warc.gz",
		w.opts.Prefix, w.seq, date.UTC().Format("20060102150405")))
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("warcwriter: create warc: %w", err)
	}
	w.file = f
	w.currentName = filepath.Base(name)
	w.currentSize = 0
	return nil
}

// writeRecord emits one record as a standalone gzip member. A fresh gzip
// writer per record guarantees an independent header, trailer and member
// boundary, which is required for random access by byte offset.
func (w *Writer) writeRecord(r *Record) (offset, length int64, err error) {
	offset = w.currentSize
	gz := gzip.NewWriter(w.file)
	if _, err := gz.Write(r.marshal()); err != nil {
		return 0, 0, fmt.Errorf("warcwriter: gzip write: %w", err)
	}
	if err := gz.Close(); err != nil {
		return 0, 0, fmt.Errorf("warcwriter: gzip close: %w", err)
	}
	pos, err := w.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, 0, fmt.Errorf("warcwriter: seek: %w", err)
	}
	w.currentSize = pos
	return offset, pos - offset, nil
}

func (w *Writer) appendIndex(e *cdxjEntry) error {
	line, err := e.line()
	if err != nil {
		return err
	}
	if _, err := w.cdxj.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("warcwriter: write cdxj: %w", err)
	}
	return nil
}

// Close flushes and releases all open files. It is safe to call repeatedly.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	var firstErr error
	if w.file != nil {
		if err := w.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		w.file = nil
	}
	if w.cdxj != nil {
		if err := w.cdxj.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		w.cdxj = nil
	}
	return firstErr
}

// CurrentFile returns the active WARC file name and its current byte size.
func (w *Writer) CurrentFile() (name string, size int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.currentName, w.currentSize
}

func requestTargetURI(req *http.Request) string {
	if req.URL.IsAbs() {
		return req.URL.String()
	}
	scheme := req.URL.Scheme
	if scheme == "" {
		scheme = "http"
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	return scheme + "://" + host + req.URL.RequestURI()
}

func contentTypeOf(resp *http.Response) string {
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		return "application/octet-stream"
	}
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	return strings.ToLower(ct)
}

// surt converts a URL into the SURT key form used by CDXJ:
// http://www.example.com/path -> com,example,www)/path
func surt(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	host := u.Hostname()
	labels := strings.Split(host, ".")
	for i, j := 0, len(labels)-1; i < j; i, j = i+1, j-1 {
		labels[i], labels[j] = labels[j], labels[i]
	}
	return strings.Join(labels, ",") + ")" + u.RequestURI()
}

func cdxjTimestamp(t time.Time) string {
	return t.UTC().Format("20060102150405")
}
