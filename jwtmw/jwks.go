package jwtmw

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"
)

// jwk is the subset of RFC 7517 JSON Web Key fields used by the verifier.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

// keyEntry is a parsed public key together with its rotation state.
type keyEntry struct {
	kid       string
	public    any
	alg       string
	retiredAt time.Time
}

type refreshCall struct {
	done chan struct{}
	err  error
}

var errRefreshRateLimited = errors.New("jwtmw: JWKS refresh rate limited")

// Refresh forces a JWKS fetch. Concurrent callers are coalesced into a single
// HTTP request.
func (v *Verifier) Refresh(ctx context.Context) error {
	return v.refreshCoalesced(ctx, false)
}

func (v *Verifier) refreshCoalesced(ctx context.Context, throttle bool) error {
	v.mu.Lock()
	if v.inflight != nil {
		call := v.inflight
		v.mu.Unlock()
		select {
		case <-call.done:
			return call.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Throttling only protects a still-fresh cache from a storm of unknown
	// kids. Expired caches and explicit Refresh calls always go through, so a
	// genuine key rotation is never blocked by the rate limiter.
	if throttle && v.cacheFreshLocked() && !v.lastAttempt.IsZero() &&
		v.now().Sub(v.lastAttempt) < v.cfg.MinRefreshInterval {
		v.mu.Unlock()
		return errRefreshRateLimited
	}

	call := &refreshCall{done: make(chan struct{})}
	v.inflight = call
	v.lastAttempt = v.now()
	v.mu.Unlock()

	err := v.doRefresh(ctx)

	v.mu.Lock()
	v.inflight = nil
	call.err = err
	close(call.done)
	v.mu.Unlock()
	return err
}

func (v *Verifier) doRefresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.cfg.JWKSURI, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("jwtmw: fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwtmw: fetch JWKS: unexpected status %s", resp.Status)
	}

	var doc jwksDocument
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJWKSBytes)).Decode(&doc); err != nil {
		return fmt.Errorf("jwtmw: decode JWKS: %w", err)
	}
	return v.applyJWKS(doc)
}

// applyJWKS swaps in the new document. Keys missing from the new document are
// kept in a retired state until GracePeriod elapses, so tokens signed during
// rotation continue to validate.
func (v *Verifier) applyJWKS(doc jwksDocument) error {
	parsed := make(map[string]*keyEntry, len(doc.Keys))
	var parseErrs []error
	for _, raw := range doc.Keys {
		if raw.Use == "enc" {
			continue
		}
		entry, err := parseJWK(raw)
		if err != nil {
			parseErrs = append(parseErrs, err)
			continue
		}
		parsed[entry.kid] = entry
	}
	if len(doc.Keys) > 0 && len(parsed) == 0 {
		return fmt.Errorf("jwtmw: JWKS contained no usable keys: %w", errors.Join(parseErrs...))
	}

	now := v.now()
	v.mu.Lock()
	defer v.mu.Unlock()

	next := make(map[string]*keyEntry, len(parsed)+len(v.keys))
	for kid, entry := range parsed {
		entry.retiredAt = time.Time{}
		next[kid] = entry
	}
	for kid, old := range v.keys {
		if _, ok := next[kid]; ok {
			continue
		}
		retired := old.retiredAt
		if retired.IsZero() {
			retired = now
		}
		if now.Sub(retired) <= v.cfg.GracePeriod {
			old.retiredAt = retired
			next[kid] = old
		}
	}

	v.keys = next
	v.fetchedAt = now
	v.expiresAt = now.Add(v.cfg.CacheTTL)
	return errors.Join(parseErrs...)
}

func (v *Verifier) cacheFreshLocked() bool {
	return !v.fetchedAt.IsZero() && !v.now().After(v.expiresAt)
}

// lookupLocked resolves a kid against the cache and reports why it is unusable.
func (v *Verifier) lookupLocked(kid string) (*keyEntry, error) {
	if v.fetchedAt.IsZero() {
		return nil, ErrJWKSUnavailable
	}
	if v.now().After(v.expiresAt) {
		return nil, ErrCacheExpired
	}
	entry, ok := v.keys[kid]
	if !ok {
		return nil, ErrUnknownKid
	}
	if !entry.retiredAt.IsZero() && v.now().Sub(entry.retiredAt) > v.cfg.GracePeriod {
		delete(v.keys, kid)
		return nil, ErrUnknownKid
	}
	return entry, nil
}

func (v *Verifier) lookup(kid string) (*keyEntry, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.lookupLocked(kid)
}

func parseJWK(raw jwk) (*keyEntry, error) {
	if raw.Kid == "" {
		return nil, errors.New("jwtmw: jwk missing kid")
	}
	entry := &keyEntry{kid: raw.Kid, alg: raw.Alg}

	switch raw.Kty {
	case "RSA":
		modulus, err := base64.RawURLEncoding.DecodeString(raw.N)
		if err != nil {
			return nil, fmt.Errorf("jwtmw: rsa jwk %q: invalid n: %w", raw.Kid, err)
		}
		exponentBytes, err := base64.RawURLEncoding.DecodeString(raw.E)
		if err != nil {
			return nil, fmt.Errorf("jwtmw: rsa jwk %q: invalid e: %w", raw.Kid, err)
		}
		exponent := new(big.Int).SetBytes(exponentBytes)
		if !exponent.IsInt64() || exponent.Int64() <= 0 {
			return nil, fmt.Errorf("jwtmw: rsa jwk %q: invalid exponent", raw.Kid)
		}
		pub := &rsa.PublicKey{
			N: new(big.Int).SetBytes(modulus),
			E: int(exponent.Int64()),
		}
		if pub.N.BitLen() < 2048 {
			return nil, fmt.Errorf("jwtmw: rsa jwk %q: modulus too short", raw.Kid)
		}
		entry.public = pub

	case "EC":
		curve, err := curveFromName(raw.Crv)
		if err != nil {
			return nil, fmt.Errorf("jwtmw: ec jwk %q: %w", raw.Kid, err)
		}
		xb, err := base64.RawURLEncoding.DecodeString(raw.X)
		if err != nil {
			return nil, fmt.Errorf("jwtmw: ec jwk %q: invalid x: %w", raw.Kid, err)
		}
		yb, err := base64.RawURLEncoding.DecodeString(raw.Y)
		if err != nil {
			return nil, fmt.Errorf("jwtmw: ec jwk %q: invalid y: %w", raw.Kid, err)
		}
		pub := &ecdsa.PublicKey{
			Curve: curve,
			X:     new(big.Int).SetBytes(xb),
			Y:     new(big.Int).SetBytes(yb),
		}
		if !curve.IsOnCurve(pub.X, pub.Y) {
			return nil, fmt.Errorf("jwtmw: ec jwk %q: point not on curve", raw.Kid)
		}
		entry.public = pub

	case "OKP":
		if raw.Crv != "Ed25519" {
			return nil, fmt.Errorf("jwtmw: okp jwk %q: unsupported crv %q", raw.Kid, raw.Crv)
		}
		rawKey, err := base64.RawURLEncoding.DecodeString(raw.X)
		if err != nil || len(rawKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("jwtmw: okp jwk %q: invalid key", raw.Kid)
		}
		entry.public = ed25519.PublicKey(rawKey)

	default:
		// Symmetric ("oct") keys are never accepted: the gateway only holds
		// public keys, which blocks RSA/HMAC algorithm confusion.
		return nil, fmt.Errorf("jwtmw: jwk %q: unsupported kty %q", raw.Kid, raw.Kty)
	}

	if entry.alg != "" && !algMatchesKey(entry.public, entry.alg) {
		return nil, fmt.Errorf("jwtmw: jwk %q: alg %q incompatible with key type", raw.Kid, entry.alg)
	}
	return entry, nil
}

func curveFromName(name string) (elliptic.Curve, error) {
	switch name {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("unsupported crv %q", name)
	}
}

func algMatchesKey(pub any, alg string) bool {
	switch pub.(type) {
	case *rsa.PublicKey:
		_, _, ok := rsaAlgSpec(alg)
		return ok
	case *ecdsa.PublicKey:
		switch alg {
		case "ES256", "ES384", "ES512":
			return true
		}
	case ed25519.PublicKey:
		return alg == "EdDSA"
	}
	return false
}
