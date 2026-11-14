package metapin

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// ErrDigestMismatch is returned when fetched content does not match the
// on-chain commitment; the collectible must not be displayed.
var ErrDigestMismatch = errors.New("metapin: fetched metadata digest does not match on-chain commitment")

// ErrUnknownToken is returned for tokens without a mint commitment.
var ErrUnknownToken = errors.New("metapin: unknown token")

// Fetcher retrieves raw metadata bytes from an external location. The
// returned bytes are always treated as untrusted.
type Fetcher interface {
	Fetch(uri string) ([]byte, error)
}

// FetcherFunc adapts a function to Fetcher.
type FetcherFunc func(uri string) ([]byte, error)

func (f FetcherFunc) Fetch(uri string) ([]byte, error) { return f(uri) }

// Service pins collectible metadata by content digest and resolves it
// against on-chain commitments.
type Service struct {
	ledger Ledger

	mu  sync.RWMutex
	uri map[string]string // tokenID -> current retrieval location
}

func NewService(ledger Ledger) *Service {
	return &Service{ledger: ledger, uri: make(map[string]string)}
}

// Mint fixes the metadata by content digest, writes the commitment on-chain
// and records retrievalURI as a mere fetch location.
func (s *Service) Mint(tokenID, owner string, md Metadata, retrievalURI string) (Digest, error) {
	digest, _, err := Commitment(md)
	if err != nil {
		return Digest{}, fmt.Errorf("metapin: mint: %w", err)
	}
	if err := s.ledger.Commit(Event{
		Kind:       EventMint,
		TokenID:    tokenID,
		Commitment: digest,
		To:         owner,
	}); err != nil {
		return Digest{}, err
	}
	s.mu.Lock()
	s.uri[tokenID] = retrievalURI
	s.mu.Unlock()
	return digest, nil
}

// resolve fetches the metadata for tokenID and verifies it against the
// on-chain commitment. Any mismatch refuses the content.
func (s *Service) resolve(tokenID string, f Fetcher) (Digest, []byte, error) {
	want, ok := s.ledger.CommitmentOf(tokenID)
	if !ok {
		return Digest{}, nil, ErrUnknownToken
	}
	s.mu.RLock()
	uri := s.uri[tokenID]
	s.mu.RUnlock()
	raw, err := f.Fetch(uri)
	if err != nil {
		return Digest{}, nil, fmt.Errorf("metapin: fetch %q: %w", uri, err)
	}
	if got := DigestOf(raw); got != want {
		return Digest{}, nil, fmt.Errorf("%w (token %q)", ErrDigestMismatch, tokenID)
	}
	return want, raw, nil
}

// Display returns the metadata for tokenID only if the bytes served at the
// retrieval location match the on-chain commitment. A tampered or replaced
// document yields ErrDigestMismatch and nothing is displayed.
func (s *Service) Display(tokenID string, f Fetcher) (*Metadata, error) {
	_, raw, err := s.resolve(tokenID, f)
	if err != nil {
		return nil, err
	}
	var md Metadata
	if err := json.Unmarshal(raw, &md); err != nil {
		return nil, fmt.Errorf("metapin: decode metadata: %w", err)
	}
	return &md, nil
}

// Transfer moves ownership, binding the transfer record to the same mint
// commitment. The content served at the retrieval location is verified
// first, so a collectible whose metadata no longer matches its commitment
// cannot change hands as if intact.
func (s *Service) Transfer(tokenID, from, to string, f Fetcher) error {
	digest, _, err := s.resolve(tokenID, f)
	if err != nil {
		return err
	}
	return s.ledger.Commit(Event{
		Kind:       EventTransfer,
		TokenID:    tokenID,
		Commitment: digest,
		From:       from,
		To:         to,
	})
}

// RecordRoyalty writes a royalty record bound to the token's mint
// commitment.
func (s *Service) RecordRoyalty(tokenID, from, to string, bps int) error {
	digest, ok := s.ledger.CommitmentOf(tokenID)
	if !ok {
		return ErrUnknownToken
	}
	return s.ledger.Commit(Event{
		Kind:       EventRoyalty,
		TokenID:    tokenID,
		Commitment: digest,
		From:       from,
		To:         to,
		RoyaltyBps: bps,
	})
}

// UpdateRetrievalURI repoints the retrieval location. The new location is
// accepted only if it already serves bytes matching the on-chain
// commitment; the commitment itself never changes.
func (s *Service) UpdateRetrievalURI(tokenID, newURI string, f Fetcher) error {
	want, ok := s.ledger.CommitmentOf(tokenID)
	if !ok {
		return ErrUnknownToken
	}
	raw, err := f.Fetch(newURI)
	if err != nil {
		return fmt.Errorf("metapin: fetch %q: %w", newURI, err)
	}
	if got := DigestOf(raw); got != want {
		return fmt.Errorf("%w (token %q, new location rejected)", ErrDigestMismatch, tokenID)
	}
	s.mu.Lock()
	s.uri[tokenID] = newURI
	s.mu.Unlock()
	return nil
}

// History exposes the chain records for a token.
func (s *Service) History(tokenID string) []Event {
	return s.ledger.History(tokenID)
}
