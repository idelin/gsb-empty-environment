package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// SSH message numbers used by global requests (RFC 4254, section 4).
const (
	SSH_MSG_GLOBAL_REQUEST  byte = 80
	SSH_MSG_REQUEST_SUCCESS byte = 81
	SSH_MSG_REQUEST_FAILURE byte = 82
)

// MarshalGlobalRequest encodes the SSH_MSG_GLOBAL_REQUEST payload:
// byte message type, string request type, boolean want-reply, raw payload.
func MarshalGlobalRequest(req Request) []byte {
	b := make([]byte, 0, 1+4+len(req.Type)+1+len(req.Payload))
	b = append(b, SSH_MSG_GLOBAL_REQUEST)
	b = appendString(b, req.Type)
	if req.WantReply {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	return append(b, req.Payload...)
}

// MarshalReply encodes an SSH_MSG_REQUEST_SUCCESS/FAILURE message. Most
// global-request replies carry no additional data.
func MarshalReply(approved bool) []byte {
	if approved {
		return []byte{SSH_MSG_REQUEST_SUCCESS}
	}
	return []byte{SSH_MSG_REQUEST_FAILURE}
}

// ParseReply decodes an SSH_MSG_REQUEST_SUCCESS/FAILURE message.
func ParseReply(msg []byte) (approved bool, ok bool) {
	if len(msg) == 0 {
		return false, false
	}
	switch msg[0] {
	case SSH_MSG_REQUEST_SUCCESS:
		return true, true
	case SSH_MSG_REQUEST_FAILURE:
		return false, true
	default:
		return false, false
	}
}

// ParseGlobalRequest decodes an SSH_MSG_GLOBAL_REQUEST message produced by
// MarshalGlobalRequest.
func ParseGlobalRequest(msg []byte) (Request, error) {
	if len(msg) == 0 || msg[0] != SSH_MSG_GLOBAL_REQUEST {
		return Request{}, errors.New("netsec: not an SSH_MSG_GLOBAL_REQUEST message")
	}
	r := bytes.NewReader(msg[1:])
	name, err := readString(r)
	if err != nil {
		return Request{}, err
	}
	want, err := r.ReadByte()
	if err != nil {
		return Request{}, err
	}
	payload, err := io.ReadAll(r)
	if err != nil {
		return Request{}, err
	}
	return Request{Type: name, WantReply: want == 1, Payload: payload}, nil
}

func appendString(b []byte, s string) []byte {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(s)))
	b = append(b, lenBuf[:]...)
	return append(b, s...)
}

func readString(r *bytes.Reader) (string, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return "", err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
