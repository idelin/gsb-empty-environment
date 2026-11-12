package metatx

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

// ErrContentChanged is returned when the source content is modified while
// the transaction is being prepared.
var ErrContentChanged = errors.New("metatx: content changed during transaction")

// Publisher promotes content from the upload area to the publish area.
type Publisher struct {
	Store  Store
	Signer Signer
}

// Publish stages src, applies policy to attrs, and commits content and
// encoded attributes under name in a single transaction. Source attributes
// are never inherited implicitly: each namespace is preserved, dropped, or
// re-signed according to policy, and re-signed values are bound to the
// final content digest. On any failure the transaction is aborted and the
// previously published version of name, if any, is left fully intact.
// It returns the digest of the committed content.
func (p *Publisher) Publish(name string, src Content, attrs []Attribute, policy Policy) ([]byte, error) {
	staged, digest, err := p.stage(src)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = p.Store.Abort(staged)
		}
	}()

	if err := verifyUnchanged(src, digest); err != nil {
		return nil, err
	}

	final, err := p.applyPolicy(attrs, policy, digest)
	if err != nil {
		return nil, err
	}

	encoded, err := EncodeAttributes(final)
	if err != nil {
		return nil, err
	}

	if err := p.Store.Commit(name, staged, encoded); err != nil {
		return nil, fmt.Errorf("metatx: commit %q: %w", name, err)
	}
	committed = true
	return digest, nil
}

// stage streams the content into the store while computing its digest.
func (p *Publisher) stage(src Content) (Staged, []byte, error) {
	rc, err := src.Open()
	if err != nil {
		return nil, nil, fmt.Errorf("metatx: open content: %w", err)
	}
	defer rc.Close()
	h := sha256.New()
	staged, err := p.Store.Stage(io.TeeReader(rc, h))
	if err != nil {
		return nil, nil, fmt.Errorf("metatx: stage content: %w", err)
	}
	return staged, h.Sum(nil), nil
}

// verifyUnchanged re-reads the source and compares digests, aborting the
// transaction if the content changed mid-way.
func verifyUnchanged(src Content, digest []byte) error {
	rc, err := src.Open()
	if err != nil {
		return fmt.Errorf("metatx: reopen content: %w", err)
	}
	defer rc.Close()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return fmt.Errorf("metatx: reread content: %w", err)
	}
	if !bytes.Equal(digest, h.Sum(nil)) {
		return ErrContentChanged
	}
	return nil
}

// applyPolicy rewrites the source attributes according to policy.
func (p *Publisher) applyPolicy(attrs []Attribute, policy Policy, digest []byte) ([]Attribute, error) {
	final := make([]Attribute, 0, len(attrs))
	for _, a := range attrs {
		switch policy.action(a.Namespace) {
		case Preserve:
			final = append(final, a)
		case Resign:
			v, err := p.Signer.Sign(digest, a.Namespace, a.Key, a.Value)
			if err != nil {
				return nil, fmt.Errorf("metatx: resign %s/%s: %w", a.Namespace, a.Key, err)
			}
			final = append(final, Attribute{Namespace: a.Namespace, Key: a.Key, Value: v})
		default:
			// Drop: source attribute is not inherited.
		}
	}
	return final, nil
}
