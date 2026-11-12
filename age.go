package netsec

import (
	"bufio"
	"crypto/rand"
	"errors"
	"io"
)

// Decrypt is the decryption entry point.
//
// The security-critical ordering is:
//
//  1. parse the header without trusting it;
//  2. recover the file key from a recipient stanza via an Identity;
//  3. verify the HMAC covering every header byte (unknown stanza types
//     included);
//  4. only then unlock the payload reader.
//
// The returned io.Reader yields plaintext exclusively after step 3; reading
// it earlier returns ErrHeaderNotAuthenticated and zero bytes.
func Decrypt(r *bufio.Reader, identities ...Identity) (*Header, *PayloadReader, error) {
	if len(identities) == 0 {
		return nil, nil, errors.New("netsec: no identities provided")
	}

	header, payload, err := ParseHeader(r)
	if err != nil {
		return nil, nil, err
	}

	fileKey, err := unwrapFileKey(header.Stanzas, identities)
	if err != nil {
		return nil, nil, err
	}

	if err := header.VerifyMAC(fileKey); err != nil {
		// The payload reader stays sealed on authentication failure.
		return nil, payload, err
	}
	return header, payload, nil
}

// unwrapFileKey tries every identity until one recovers the file key. A
// malformed stanza is a hard error; "not for me" answers let the search
// continue.
func unwrapFileKey(stanzas []*Stanza, identities []Identity) ([]byte, error) {
	var lastErr error
	for _, identity := range identities {
		fileKey, err := identity.Unwrap(stanzas)
		if err == nil {
			return fileKey, nil
		}
		if !errors.Is(err, ErrIncorrectIdentity) {
			return nil, err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = ErrIncorrectIdentity
	}
	return nil, lastErr
}

// Encrypt writes a header for the given recipients to w and returns a writer
// for the plaintext payload. The caller must Close the returned writer.
func Encrypt(w io.Writer, recipients ...Recipient) (io.WriteCloser, error) {
	if len(recipients) == 0 {
		return nil, errors.New("netsec: no recipients provided")
	}

	fileKey := make([]byte, FileKeySize)
	if _, err := rand.Read(fileKey); err != nil {
		return nil, err
	}

	header := &Header{}
	var hasScrypt bool
	for _, recipient := range recipients {
		stanzas, err := recipient.Wrap(fileKey)
		if err != nil {
			return nil, err
		}
		for _, s := range stanzas {
			if s.Type == scryptType {
				if hasScrypt || len(recipients) != 1 {
					return nil, errors.New("netsec: scrypt recipient must be the only recipient")
				}
				hasScrypt = true
			}
			header.Stanzas = append(header.Stanzas, s)
		}
	}

	// Build the authenticated prefix so the MAC line can be emitted.
	header.raw = header.canonicalRaw()
	encoded, err := header.Marshal(fileKey)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(encoded); err != nil {
		return nil, err
	}

	key, err := payloadKeyFromFileKey(fileKey)
	if err != nil {
		return nil, err
	}
	return newStreamWriter(w, key)
}

// canonicalRaw reproduces the authenticated header prefix (everything up to
// and including "---") from the parsed stanzas for freshly generated headers.
func (h *Header) canonicalRaw() []byte {
	raw := make([]byte, 0)
	raw = append(raw, Version...)
	raw = append(raw, '\n')
	for _, s := range h.Stanzas {
		raw = append(raw, MarshalStanza(s)...)
	}
	raw = append(raw, "---"...)
	return raw
}
