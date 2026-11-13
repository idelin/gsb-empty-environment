package netsec

import (
	"errors"
	"fmt"
)

const (
	handshakeTypeClientHello = 1
	tlsRecordTypeHandshake   = 22
	extensionPreSharedKey    = 41
)

// TruncateClientHelloBinders returns the ClientHello prefix used as the
// binder transcript and the binder values carried by the pre_shared_key
// extension. The prefix ends immediately before the two-byte length of the
// binders list, matching RFC 8446, Section 4.2.11.2 and crypto/tls.
//
// The input may be a handshake message or a single TLS record carrying one.
func TruncateClientHelloBinders(clientHello []byte) (truncated []byte, binders [][]byte, err error) {
	return truncateClientHelloBinders(clientHello)
}

func truncateClientHelloBinders(clientHello []byte) ([]byte, [][]byte, error) {
	msg, err := unwrapHandshakeRecord(clientHello)
	if err != nil {
		return nil, nil, err
	}
	if len(msg) < 4 {
		return nil, nil, errors.New("netsec: ClientHello is too short")
	}
	if msg[0] != handshakeTypeClientHello {
		return nil, nil, fmt.Errorf("netsec: not a ClientHello (handshake type %d)", msg[0])
	}
	handshakeLen := int(msg[1])<<16 | int(msg[2])<<8 | int(msg[3])
	if handshakeLen != len(msg)-4 {
		return nil, nil, errors.New("netsec: invalid ClientHello length")
	}

	pos := 4
	need := func(n int) error {
		if n < 0 || pos+n > len(msg) {
			return errors.New("netsec: truncated ClientHello")
		}
		return nil
	}

	pos += 2 // legacy_version
	if err := need(32); err != nil {
		return nil, nil, err
	}
	pos += 32 // random

	if err := need(1); err != nil {
		return nil, nil, err
	}
	sessionIDLen := int(msg[pos])
	pos++
	if err := need(sessionIDLen); err != nil {
		return nil, nil, err
	}
	pos += sessionIDLen

	if err := need(2); err != nil {
		return nil, nil, err
	}
	cipherSuitesLen := int(msg[pos])<<8 | int(msg[pos+1])
	pos += 2
	if err := need(cipherSuitesLen); err != nil {
		return nil, nil, err
	}
	pos += cipherSuitesLen

	if err := need(1); err != nil {
		return nil, nil, err
	}
	compressionLen := int(msg[pos])
	pos++
	if err := need(compressionLen); err != nil {
		return nil, nil, err
	}
	pos += compressionLen

	if err := need(2); err != nil {
		return nil, nil, err
	}
	extensionsLen := int(msg[pos])<<8 | int(msg[pos+1])
	pos += 2
	if err := need(extensionsLen); err != nil {
		return nil, nil, err
	}
	extensionsEnd := pos + extensionsLen

	for pos < extensionsEnd {
		if err := need(4); err != nil {
			return nil, nil, err
		}
		extType := int(msg[pos])<<8 | int(msg[pos+1])
		extLen := int(msg[pos+2])<<8 | int(msg[pos+3])
		dataStart := pos + 4
		dataEnd := dataStart + extLen
		if dataEnd > extensionsEnd {
			return nil, nil, errors.New("netsec: invalid extension length")
		}

		if extType == extensionPreSharedKey {
			if dataEnd != extensionsEnd {
				return nil, nil, errors.New("netsec: pre_shared_key is not the last extension")
			}
			if dataStart+2 > dataEnd {
				return nil, nil, errors.New("netsec: truncated PSK identities")
			}
			identitiesLen := int(msg[dataStart])<<8 | int(msg[dataStart+1])
			identitiesEnd := dataStart + 2 + identitiesLen
			if identitiesEnd > dataEnd {
				return nil, nil, errors.New("netsec: invalid PSK identities length")
			}
			if identitiesEnd+2 > dataEnd {
				return nil, nil, errors.New("netsec: missing PSK binder length")
			}
			bindersLen := int(msg[identitiesEnd])<<8 | int(msg[identitiesEnd+1])
			bindersEnd := identitiesEnd + 2 + bindersLen
			if bindersEnd != dataEnd {
				return nil, nil, errors.New("netsec: invalid PSK binders length")
			}

			var binders [][]byte
			for p := identitiesEnd + 2; p < bindersEnd; {
				if p+1 > bindersEnd {
					return nil, nil, errors.New("netsec: truncated PSK binder")
				}
				n := int(msg[p])
				p++
				if n == 0 || p+n > bindersEnd {
					return nil, nil, errors.New("netsec: invalid PSK binder length")
				}
				binders = append(binders, append([]byte(nil), msg[p:p+n]...))
				p += n
			}
			return msg[:identitiesEnd], binders, nil
		}
		pos = dataEnd
	}
	return nil, nil, errors.New("netsec: ClientHello has no pre_shared_key extension")
}

func unwrapHandshakeRecord(in []byte) ([]byte, error) {
	if len(in) == 0 || in[0] != tlsRecordTypeHandshake {
		return in, nil
	}
	var handshake []byte
	for len(in) > 0 {
		if len(in) < 5 {
			return nil, errors.New("netsec: truncated TLS record")
		}
		if in[0] != tlsRecordTypeHandshake {
			return nil, fmt.Errorf("netsec: unexpected TLS record type %d", in[0])
		}
		n := int(in[3])<<8 | int(in[4])
		if n == 0 || 5+n > len(in) {
			return nil, errors.New("netsec: invalid TLS record length")
		}
		handshake = append(handshake, in[5:5+n]...)
		in = in[5+n:]
	}
	return handshake, nil
}
