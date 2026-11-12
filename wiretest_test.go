package netsec

import (
	"bytes"
	"encoding/binary"
)

func smb2Header(command uint16) []byte {
	header := make([]byte, smb2HeaderLen)
	binary.LittleEndian.PutUint32(header[0:4], protocolSMB2)
	binary.LittleEndian.PutUint16(header[4:6], 64) // StructureSize
	binary.LittleEndian.PutUint32(header[12:16], uint32(command))
	return header
}

func negotiateContext(ctxType uint16, data []byte) []byte {
	entry := make([]byte, negotiateCtxHdrLen+len(data))
	binary.LittleEndian.PutUint16(entry[0:2], ctxType)
	binary.LittleEndian.PutUint16(entry[2:4], uint16(len(data)))
	copy(entry[4:], data)
	return entry
}

func preauthContext() []byte {
	data := make([]byte, 6)
	binary.LittleEndian.PutUint16(data[0:2], 1) // HashAlgorithmCount
	binary.LittleEndian.PutUint16(data[2:4], 0) // SaltLength
	binary.LittleEndian.PutUint16(data[4:6], preauthSHA512)
	return negotiateContext(ctxPreauthIntegrity, data)
}

func encryptionContext(ciphers []uint16) []byte {
	data := make([]byte, 2+2*len(ciphers))
	binary.LittleEndian.PutUint16(data[0:2], uint16(len(ciphers)))
	for i, cipher := range ciphers {
		binary.LittleEndian.PutUint16(data[2+2*i:], cipher)
	}
	return negotiateContext(ctxEncryption, data)
}

func negotiateRequest(dialects []uint16, capabilities uint32, contexts ...[]byte) []byte {
	body := make([]byte, negotiateReqBaseLen+2*len(dialects))
	binary.LittleEndian.PutUint16(body[0:2], 36)
	binary.LittleEndian.PutUint16(body[negDialectCountOff:], uint16(len(dialects)))
	binary.LittleEndian.PutUint16(body[negSecurityModeOff:], 1)
	binary.LittleEndian.PutUint32(body[negCapabilitiesOff:], capabilities)
	for i, dialect := range dialects {
		binary.LittleEndian.PutUint16(body[negDialectsStartOff+2*i:], dialect)
	}

	message := append(smb2Header(cmdNegotiate), body...)
	if len(contexts) == 0 {
		return message
	}

	ctxArea := bytes.NewBuffer(nil)
	for i, ctx := range contexts {
		ctxArea.Write(ctx)
		if i < len(contexts)-1 {
			if pad := (8 - ctxArea.Len()%8) % 8; pad > 0 {
				ctxArea.Write(make([]byte, pad))
			}
		}
	}

	ctxOffset := len(message)
	binary.LittleEndian.PutUint32(message[smb2HeaderLen+negCtxOffsetOff:], uint32(ctxOffset))
	binary.LittleEndian.PutUint16(message[smb2HeaderLen+negCtxCountOff:], uint16(len(contexts)))
	message = append(message, ctxArea.Bytes()...)
	return message
}

func negotiateResponse() []byte {
	body := make([]byte, 65)
	binary.LittleEndian.PutUint16(body[0:2], 65)
	return append(smb2Header(cmdNegotiate), body...)
}

func plainRequest(command uint16) []byte {
	return smb2Header(command)
}

func encodeUTF16(s string) []byte {
	out := make([]byte, 0, 2*len(s))
	for _, r := range s {
		buf := make([]byte, 2)
		binary.LittleEndian.PutUint16(buf, uint16(r))
		out = append(out, buf...)
	}
	return out
}

func treeConnectRequest(path string) []byte {
	utf16Path := encodeUTF16(path)
	body := make([]byte, treePathStart)         // fixed part ends where the path buffer begins
	binary.LittleEndian.PutUint16(body[0:2], 9) // SMB2 TREE_CONNECT StructureSize is 9
	pathOffset := smb2HeaderLen + treePathStart // offset relative to the SMB2 header
	binary.LittleEndian.PutUint32(body[treePathOffOff:], uint32(pathOffset))
	binary.LittleEndian.PutUint32(body[treePathLenOff:], uint32(len(utf16Path)))

	message := append(smb2Header(cmdTreeConnect), body...)
	return append(message, utf16Path...)
}

func transformMessage() []byte {
	message := make([]byte, transformHeaderLen)
	binary.LittleEndian.PutUint32(message[0:4], protocolTransform)
	return message
}

func acceptVerifier(seen *[]byte) SessionSetupVerifier {
	return func(_, hash []byte) bool {
		*seen = append((*seen)[:0], hash...)
		return true
	}
}

func rejectVerifier() SessionSetupVerifier {
	return func(_, _ []byte) bool { return false }
}

func expectDecision(t testingT, got Decision, action Action, reason Reason) {
	t.Helper()
	if got.Action != action || got.Reason != reason {
		t.Fatalf("decision = (action=%v reason=%v), want (action=%v reason=%v)",
			got.Action, got.Reason, action, reason)
	}
}

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
}
