package netsec

import (
	"encoding/binary"
	"unicode/utf16"
)

// negotiateInfo is the validated subset of an SMB2 NEGOTIATE request used by
// the policy engine.
type negotiateInfo struct {
	dialects     []uint16
	capabilities uint32
	offers311    bool
	hasPreauth   bool
	supportsSHA  bool
	ciphers      []uint16
}

// handleNegotiate applies the downgrade-protection policy to a negotiate
// request and folds the wire message into the preauth hash.
func (e *Engine) handleNegotiate(message []byte) Decision {
	e.state = stateNegotiating

	info, ok := parseNegotiate(message)
	if !ok {
		return e.close(ActionDisconnect, ReasonMalformedMessage)
	}

	// A client that offers SMB 3.1.1 must always carry the pre-auth integrity
	// and encryption contexts, even if the server ultimately selects a lower
	// dialect from the offered list.
	if info.offers311 {
		if !info.hasPreauth {
			return e.close(ActionDisconnect, ReasonNoPreauthContext)
		}
		if !info.supportsSHA {
			return e.close(ActionDisconnect, ReasonNoSHA512Preauth)
		}
		if len(info.ciphers) == 0 {
			return e.close(ActionDisconnect, ReasonNoEncryptionCipher)
		}
	}

	dialect, ok := selectDialect(info.dialects, e.dialects)
	if !ok {
		return e.close(ActionReject, ReasonNoCommonDialect)
	}

	if dialect == DialectSMB311 {
		// 3.1.1 negotiates the hash and cipher through negotiate contexts.
		if !info.hasPreauth {
			return e.close(ActionDisconnect, ReasonNoPreauthContext)
		}
		if !info.supportsSHA {
			return e.close(ActionDisconnect, ReasonNoSHA512Preauth)
		}

		cipher, cipherOK := selectCipher(info.ciphers, e.ciphers)
		if len(info.ciphers) == 0 {
			return e.close(ActionDisconnect, ReasonNoEncryptionCipher)
		}
		if !cipherOK {
			// Client cipher list and server preference have no intersection.
			return e.close(ActionReject, ReasonCipherNoIntersection)
		}

		e.negotiatedDialect = dialect
		e.negotiatedCipher = cipher
	} else if dialect >= DialectSMB300 {
		// 3.0 and 3.0.2 signal encryption via the SMB2_ENCRYPTION capability.
		if info.capabilities&capEncryption == 0 {
			if e.requireEncryption {
				return e.close(ActionDisconnect, ReasonNoEncryptionCipher)
			}
			e.negotiatedDialect = dialect
			e.negotiatedCipher = 0
		} else {
			e.negotiatedDialect = dialect
			e.negotiatedCipher = e.ciphers[0]
		}
	} else {
		// SMB 2.0.2 / 2.1 cannot encrypt at all.
		if e.requireEncryption {
			return e.close(ActionDisconnect, ReasonEncryptedDialectUnsupported)
		}
		e.negotiatedDialect = dialect
		e.negotiatedCipher = 0
	}

	e.preauth.fold(message)
	e.state = stateNegotiated
	return e.allow()
}

// ObserveNegotiateResponse folds the full negotiate response wire message into
// the pre-auth integrity hash. The caller invokes it once, after the server has
// built the exact response bytes it will send. Negotiation must already have
// been accepted via HandleRequest.
func (e *Engine) ObserveNegotiateResponse(message []byte) Decision {
	if e.state == stateClosed {
		return e.decide(ActionDisconnect, ReasonUnexpectedCommand)
	}
	if e.state != stateNegotiated && e.state != stateNegotiating {
		return e.decide(ActionDisconnect, ReasonUnexpectedCommand)
	}
	if len(message) < smb2HeaderLen {
		return e.close(ActionDisconnect, ReasonMalformedMessage)
	}
	command := binary.LittleEndian.Uint16(message[12:14])
	if command != cmdNegotiate {
		return e.close(ActionDisconnect, ReasonMalformedMessage)
	}
	e.preauth.fold(message)
	return e.allow()
}

// handleSessionSetup hands the raw request and the running preauth hash to the
// caller-injected verifier. A failed or missing verifier drops the connection.
func (e *Engine) handleSessionSetup(message []byte) Decision {
	if e.verifySessionSetup == nil {
		return e.close(ActionDisconnect, ReasonVerifierMissing)
	}
	if !e.verifySessionSetup(message, e.preauth.value()) {
		return e.close(ActionDisconnect, ReasonSignatureFailed)
	}
	e.state = stateSession
	return e.allow()
}

// handleTreeConnect records the encryption requirement of the target share and
// enforces the negotiated dialect/cipher when encryption is required.
func (e *Engine) handleTreeConnect(message []byte) Decision {
	if len(message) < smb2HeaderLen+treeConnectReqLen {
		return e.close(ActionDisconnect, ReasonMalformedMessage)
	}

	path, ok := parseTreePath(message)
	if !ok {
		return e.close(ActionDisconnect, ReasonMalformedMessage)
	}

	required := e.requireEncryption || e.shareEncryption(path)
	if required {
		if e.negotiatedDialect < DialectSMB300 {
			return e.close(ActionDisconnect, ReasonEncryptedDialectUnsupported)
		}
		if e.negotiatedCipher == 0 {
			return e.close(ActionDisconnect, ReasonNoEncryptionCipher)
		}
	}

	e.encryptionRequired = required
	e.state = stateTreeConnected
	return e.allow()
}

// selectDialect returns the highest-ranked server dialect that the client also
// offered. Both lists are assumed to be ordered highest preference first.
func selectDialect(clientOffers, serverDialects []uint16) (uint16, bool) {
	for _, candidate := range serverDialects {
		for _, offered := range clientOffers {
			if offered == candidate {
				return candidate, true
			}
		}
	}
	return 0, false
}

// selectCipher returns the highest-ranked server cipher that the client also
// advertised.
func selectCipher(clientCiphers, serverCiphers []uint16) (uint16, bool) {
	for _, candidate := range serverCiphers {
		for _, offered := range clientCiphers {
			if offered == candidate {
				return candidate, true
			}
		}
	}
	return 0, false
}

// parseNegotiate bounds-checks and extracts the policy-relevant fields of an
// SMB2 NEGOTIATE request.
func parseNegotiate(message []byte) (negotiateInfo, bool) {
	var info negotiateInfo
	if len(message) < smb2HeaderLen+negotiateReqBaseLen {
		return info, false
	}

	body := message[smb2HeaderLen:]
	dialectCount := int(binary.LittleEndian.Uint16(body[negDialectCountOff:]))
	if dialectCount == 0 {
		return info, false
	}

	dialectsEnd := negDialectsStartOff + dialectCount*2
	if len(body) < dialectsEnd {
		return info, false
	}
	info.dialects = make([]uint16, dialectCount)
	for i := 0; i < dialectCount; i++ {
		off := negDialectsStartOff + i*2
		dialect := binary.LittleEndian.Uint16(body[off:])
		info.dialects[i] = dialect
		if dialect == DialectSMB311 {
			info.offers311 = true
		}
	}

	info.capabilities = binary.LittleEndian.Uint32(body[negCapabilitiesOff:])

	ctxCount := int(binary.LittleEndian.Uint16(body[negCtxCountOff:]))
	ctxOffset := int(binary.LittleEndian.Uint32(body[negCtxOffsetOff:]))
	if ctxCount == 0 {
		// SMB 3.0.x and earlier legitimately carry no contexts.
		return info, true
	}
	if ctxOffset < smb2HeaderLen || ctxOffset > len(message) {
		return info, false
	}

	// Context offset is relative to the start of the SMB2 header.
	contexts := message[ctxOffset:]
	cursor := 0
	for i := 0; i < ctxCount; i++ {
		if len(contexts)-cursor < negotiateCtxHdrLen {
			return info, false
		}
		ctxType := binary.LittleEndian.Uint16(contexts[cursor+ctxTypeOff:])
		dataLen := int(binary.LittleEndian.Uint16(contexts[cursor+ctxDataLenOff:]))
		entryLen := negotiateCtxHdrLen + dataLen
		if len(contexts)-cursor < entryLen {
			return info, false
		}
		data := contexts[cursor+ctxDataOff : cursor+entryLen]

		switch ctxType {
		case ctxPreauthIntegrity:
			info.hasPreauth = true
			// Data layout: uint16 count, uint16 salt length, algorithm ids.
			hashCount := int(binary.LittleEndian.Uint16(data[0:2]))
			if hashCount == 0 || 4+hashCount*2 > dataLen {
				return info, false
			}
			for j := 0; j < hashCount; j++ {
				if binary.LittleEndian.Uint16(data[4+j*2:]) == preauthSHA512 {
					info.supportsSHA = true
				}
			}
		case ctxEncryption:
			if dataLen < 2 {
				return info, false
			}
			cipherCount := int(binary.LittleEndian.Uint16(data[0:2]))
			if 2+cipherCount*2 > dataLen {
				return info, false
			}
			info.ciphers = make([]uint16, cipherCount)
			for j := 0; j < cipherCount; j++ {
				info.ciphers[j] = binary.LittleEndian.Uint16(data[2+j*2:])
			}
		}

		cursor += entryLen
		// Contexts are 8-byte aligned relative to the context area start.
		if i < ctxCount-1 {
			pad := (8 - cursor%8) % 8
			if len(contexts)-cursor < pad {
				return info, false
			}
			cursor += pad
		}
	}

	return info, true
}

// parseTreePath extracts and UTF-16-decodes the share path from a TREE_CONNECT
// request. The path offset is relative to the start of the SMB2 header.
func parseTreePath(message []byte) (string, bool) {
	body := message[smb2HeaderLen:]
	pathOffset := int(binary.LittleEndian.Uint32(body[treePathOffOff:]))
	pathLen := int(binary.LittleEndian.Uint32(body[treePathLenOff:]))
	if pathLen%2 != 0 || pathLen == 0 {
		return "", false
	}
	// SMB2 buffer offsets are relative to the start of the SMB2 header.
	if pathOffset < smb2HeaderLen || pathOffset+pathLen > len(message) {
		return "", false
	}

	raw := message[pathOffset : pathOffset+pathLen]
	words := make([]uint16, pathLen/2)
	for i := range words {
		words[i] = binary.LittleEndian.Uint16(raw[i*2:])
	}
	runes := utf16.Decode(words)
	return string(runes), true
}
