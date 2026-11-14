package netsec

import (
	"bytes"
	"crypto/subtle"
	"errors"
)

// Watermark fields embedded in every exported file. The statement is human
// readable (attribution + scope fingerprint); the tag proves it was produced
// by this service and was not modified.
const (
	watermarkPrefix   = "# GSB-WATERMARK-1 "
	watermarkTagLabel = "# GSB-WATERMARK-TAG-1 "
)

// ErrWatermarkMismatch means an embedded watermark statement and its tag do
// not authenticate, or the expected watermark is absent.
var ErrWatermarkMismatch = errors.New("netsec: watermark verification failed")

// Watermark is the provenance header of an export.
type Watermark struct {
	TaskID      string
	PrincipalID string
	TenantID    string
	Dataset     string
	Scope       string
	Statement   string
	Tag         string
}

// buildWatermark constructs the visible statement and its keyed tag.
func buildWatermark(masterKey []byte, task *Task) Watermark {
	statement := watermarkPrefix +
		"task=" + task.ID +
		" principal=" + task.PrincipalID +
		" tenant=" + task.TenantID +
		" dataset=" + task.Dataset +
		" scope=" + canonicalScope(task.Filters)
	tag := watermarkMAC(masterKey, statement)
	return Watermark{
		TaskID:      task.ID,
		PrincipalID: task.PrincipalID,
		TenantID:    task.TenantID,
		Dataset:     task.Dataset,
		Scope:       canonicalScope(task.Filters),
		Statement:   statement,
		Tag:         tag,
	}
}

// header renders the two leading CSV comment lines.
func (w Watermark) header() []byte {
	var buf bytes.Buffer
	buf.WriteString(w.Statement)
	buf.WriteByte('\n')
	buf.WriteString(watermarkTagLabel)
	buf.WriteString(w.Tag)
	buf.WriteByte('\n')
	return buf.Bytes()
}

// verifyWatermark checks that the first two lines of a decrypted export carry
// a watermark whose statement matches the expected task scope and whose HMAC
// tag authenticates under the master key.
func verifyWatermark(masterKey, content []byte, expected *Task) error {
	lines := bytes.SplitN(content, []byte("\n"), 3)
	if len(lines) < 2 {
		return ErrWatermarkMismatch
	}
	statement := lines[0]
	tagLine := lines[1]

	want := buildWatermark(masterKey, expected)
	if subtle.ConstantTimeCompare(statement, []byte(want.Statement)) != 1 {
		return ErrWatermarkMismatch
	}
	wantTag := []byte(watermarkTagLabel + want.Tag)
	if subtle.ConstantTimeCompare(tagLine, wantTag) != 1 {
		return ErrWatermarkMismatch
	}
	return nil
}
