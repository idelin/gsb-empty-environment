// Package mimeingest streams a MIME message from an io.Reader, decoding
// nested multipart structure and transfer encodings while enforcing a
// shared decoded-byte budget across the whole message.
//
// Leaf parts classified as attachments are staged in a Store and streamed
// through a virus Scanner as their bytes are decoded. Staged objects are
// published only after the entire message has been proven structurally
// valid and every scanner session has returned a clean verdict; on any
// failure all staged objects are withdrawn and nothing is published.
package mimeingest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	// ErrBudgetExceeded is returned when the total number of decoded
	// bytes in a message exceeds Config.Budget.
	ErrBudgetExceeded = errors.New("mimeingest: decoded byte budget exceeded")
	// ErrTrailingData is returned when non-whitespace data follows the
	// final boundary of the top-level multipart, which indicates a
	// forged inner terminator line or a corrupted tail.
	ErrTrailingData = errors.New("mimeingest: unexpected data after final boundary")
	// ErrUnterminated is returned when the stream ends before the
	// enclosing multipart boundary is found.
	ErrUnterminated = errors.New("mimeingest: stream ended before enclosing boundary")
	// ErrInfected is returned when the scanner rejects an attachment.
	ErrInfected = errors.New("mimeingest: attachment rejected by scanner")
)

const (
	defaultBudget   = 64 << 20
	defaultMaxDepth = 32
	maxParts        = 10000
	maxLineLength   = 1 << 20
)

// Config controls a single Ingest call.
type Config struct {
	// Store receives staged attachment payloads. Required.
	Store Store
	// Scanner inspects attachment payloads. May be nil, in which case
	// attachments are accepted without scanning.
	Scanner Scanner
	// Budget is the maximum total number of decoded bytes allowed for
	// the whole message. Values <= 0 select a 64 MiB default.
	Budget int64
	// MaxDepth limits multipart nesting. Values <= 0 select 32.
	MaxDepth int
}

// Result summarizes a successfully ingested message.
type Result struct {
	// Attachments lists the attachments that were published, in order.
	Attachments []Attachment
	// DecodedBytes is the total number of decoded bytes charged against
	// the budget, including non-attachment bodies.
	DecodedBytes int64
}

// Ingest streams the message read from r, stages and scans its
// attachments, and publishes them only if the whole message is
// structurally valid and every scan verdict is clean. On any error all
// staged objects are withdrawn and no object is published.
func Ingest(ctx context.Context, r io.Reader, cfg Config) (*Result, error) {
	if cfg.Store == nil {
		return nil, errors.New("mimeingest: Config.Store is required")
	}
	if cfg.Budget <= 0 {
		cfg.Budget = defaultBudget
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = defaultMaxDepth
	}
	w := &walker{
		ctx:    ctx,
		br:     bufio.NewReaderSize(r, 32*1024),
		cfg:    &cfg,
		budget: &budget{limit: cfg.Budget},
		result: &Result{},
	}
	if err := w.run(); err != nil {
		w.withdrawAll()
		return nil, err
	}
	if err := w.publishAll(); err != nil {
		return nil, err
	}
	w.result.DecodedBytes = w.budget.used
	return w.result, nil
}

func (w *walker) run() error {
	hdr, err := readHeader(w.br)
	if err != nil {
		return fmt.Errorf("mimeingest: reading message headers: %w", err)
	}
	mediaType, params, err := contentType(hdr)
	if err != nil {
		return err
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		b := params["boundary"]
		if b == "" {
			return fmt.Errorf("mimeingest: %s message missing boundary parameter", mediaType)
		}
		_, _, err := w.walkMultipart(b, nil, 1)
		return err
	}
	return w.walkRootLeaf(hdr, mediaType, params)
}

func (w *walker) publishAll() error {
	for i, obj := range w.staged {
		if err := obj.Publish(w.ctx); err != nil {
			for _, rest := range w.staged[i+1:] {
				_ = rest.Withdraw(w.ctx)
			}
			return fmt.Errorf("mimeingest: publishing attachment: %w", err)
		}
	}
	return nil
}

func (w *walker) withdrawAll() {
	for _, obj := range w.staged {
		_ = obj.Withdraw(w.ctx)
	}
	w.staged = nil
}
