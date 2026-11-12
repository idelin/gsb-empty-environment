package netsec

import (
	"encoding/binary"
	"fmt"
)

// SSH agent protocol message types used by this package.
const (
	AgentcSignRequest      byte = 11
	AgentcExtension        byte = 27
	AgentConstraintExt     byte = 0xff
	SSH2MsgUserAuthRequest byte = 50
)

// Conventional spellings used in OpenSSH protocol documents.
const (
	SSH_AGENTC_SIGN_REQUEST       = AgentcSignRequest
	SSH_AGENTC_EXTENSION          = AgentcExtension
	SSH_AGENT_CONSTRAIN_EXTENSION = AgentConstraintExt
	SSH2_MSG_USERAUTH_REQUEST     = SSH2MsgUserAuthRequest
)

const (
	sessionBindExtension         = "session-bind@openssh.com"
	restrictDestinationExtension = "restrict-destination-v00@openssh.com"
	publicKeyHostBoundMethod     = "publickey-hostbound-v00@openssh.com"
	publicKeyMethod              = "publickey"
	sshConnectionService         = "ssh-connection"
)

// wireReader is a strict decoder for the SSH wire representation defined by
// RFC 4251. In particular, every string carries an explicit uint32 length; a
// caller must never split these values on textual delimiters.
type wireReader struct {
	buf []byte
	off int
}

func newWireReader(buf []byte) *wireReader {
	return &wireReader{buf: buf}
}

func (r *wireReader) remaining() int {
	return len(r.buf) - r.off
}

func (r *wireReader) readByte() (byte, error) {
	if r.remaining() < 1 {
		return 0, fmt.Errorf("ssh wire: truncated byte")
	}
	v := r.buf[r.off]
	r.off++
	return v, nil
}

func (r *wireReader) readUint32() (uint32, error) {
	if r.remaining() < 4 {
		return 0, fmt.Errorf("ssh wire: truncated uint32")
	}
	v := binary.BigEndian.Uint32(r.buf[r.off : r.off+4])
	r.off += 4
	return v, nil
}

func (r *wireReader) readString() ([]byte, error) {
	n, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	if uint64(n) > uint64(r.remaining()) {
		return nil, fmt.Errorf("ssh wire: string length %d exceeds %d remaining bytes", n, r.remaining())
	}
	v := r.buf[r.off : r.off+int(n)]
	r.off += int(n)
	return v, nil
}

func (r *wireReader) readStringValue() (string, error) {
	v, err := r.readString()
	if err != nil {
		return "", err
	}
	return string(v), nil
}

func (r *wireReader) readBool() (bool, error) {
	v, err := r.readByte()
	if err != nil {
		return false, err
	}
	switch v {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("ssh wire: invalid boolean value %d", v)
	}
}

func (r *wireReader) expectEnd() error {
	if r.remaining() != 0 {
		return fmt.Errorf("ssh wire: %d trailing byte(s)", r.remaining())
	}
	return nil
}

func appendUint32(dst []byte, v uint32) []byte {
	return append(dst, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func appendString(dst, v []byte) []byte {
	dst = appendUint32(dst, uint32(len(v)))
	return append(dst, v...)
}

func appendStringValue(dst []byte, v string) []byte {
	return appendString(dst, []byte(v))
}

func appendBool(dst []byte, v bool) []byte {
	if v {
		return append(dst, 1)
	}
	return append(dst, 0)
}

// stripAgentFrame accepts either an agent request already stripped of its
// four-byte length prefix or a complete length-prefixed agent packet. It
// returns the message body (the byte after the packet length).
func stripAgentFrame(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty ssh agent message")
	}
	// Agent message bodies always start with a one-byte type. A framed
	// message starts with a uint32 whose value must equal len(raw)-4.
	if len(raw) >= 4 && raw[0] == 0 {
		n := binary.BigEndian.Uint32(raw[:4])
		if uint64(n) == uint64(len(raw)-4) {
			return raw[4:], nil
		}
	}
	return raw, nil
}
