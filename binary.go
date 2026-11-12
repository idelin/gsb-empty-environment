package netsec

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	binaryRequestMagic  = 0x80
	binaryResponseMagic = 0x81
	binaryHeaderLength  = 24
)

type binaryOp struct {
	name         string
	extras       int
	keyRequired  bool
	keyAllowed   bool
	valueAllowed bool
	response     ResponseKind
	closeAfter   bool
}

var binaryOps = map[byte]binaryOp{
	0x00: {"get", 0, true, true, false, ResponseSingle, false},
	0x01: {"set", 8, true, true, true, ResponseSingle, false},
	0x02: {"add", 8, true, true, true, ResponseSingle, false},
	0x03: {"replace", 8, true, true, true, ResponseSingle, false},
	0x04: {"delete", 0, true, true, false, ResponseSingle, false},
	0x05: {"incr", 20, true, true, false, ResponseSingle, false},
	0x06: {"decr", 20, true, true, false, ResponseSingle, false},
	0x07: {"quit", 0, false, false, false, ResponseNone, true},
	0x08: {"flush", 4, false, false, false, ResponseSingle, false},
	0x09: {"getq", 0, true, true, false, ResponseNone, false},
	0x0a: {"noop", 0, false, false, false, ResponseSingle, false},
	0x0b: {"version", 0, false, false, false, ResponseSingle, false},
	0x0c: {"getk", 0, true, true, false, ResponseSingle, false},
	0x0d: {"getkq", 0, true, true, false, ResponseNone, false},
	0x0e: {"append", 0, true, true, true, ResponseSingle, false},
	0x0f: {"prepend", 0, true, true, true, ResponseSingle, false},
	0x11: {"setq", 8, true, true, true, ResponseNone, false},
	0x12: {"addq", 8, true, true, true, ResponseNone, false},
	0x13: {"replaceq", 8, true, true, true, ResponseNone, false},
	0x14: {"deleteq", 0, true, true, false, ResponseNone, false},
	0x15: {"incrq", 20, true, true, false, ResponseNone, false},
	0x16: {"decrq", 20, true, true, false, ResponseNone, false},
	0x17: {"quitq", 0, false, false, false, ResponseNone, true},
	0x18: {"flushq", 4, false, false, false, ResponseNone, false},
	0x19: {"appendq", 0, true, true, true, ResponseNone, false},
	0x1a: {"prependq", 0, true, true, true, ResponseNone, false},
	0x1b: {"verbosity", 4, false, false, false, ResponseSingle, false},
	0x1c: {"touch", 4, true, true, false, ResponseSingle, false},
	0x1d: {"gat", 4, true, true, false, ResponseSingle, false},
	0x1e: {"gatq", 4, true, true, false, ResponseNone, false},
	0x23: {"gatk", 4, true, true, false, ResponseSingle, false},
	0x24: {"gatkq", 4, true, true, false, ResponseNone, false},
}

func (s *Session) readBinaryRequest() (*Request, error) {
	header := make([]byte, binaryHeaderLength)
	if _, err := io.ReadFull(s.r, header); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("%w: incomplete binary header", ErrTruncated)
		}
		return nil, err
	}
	if header[0] != binaryRequestMagic {
		return nil, fmt.Errorf("%w: binary request magic is %#x", ErrMalformedRequest, header[0])
	}
	if header[5] != 0 {
		return nil, fmt.Errorf("%w: unsupported binary data type %d", ErrMalformedRequest, header[5])
	}

	keyLen := int(binary.BigEndian.Uint16(header[2:4]))
	extrasLen := int(header[4])
	bodyLen := uint64(binary.BigEndian.Uint32(header[8:12]))
	maxBody := uint64(s.n.cfg.MaxValueLength) + MaxProtocolKeyLength + 64
	if bodyLen > maxBody {
		return nil, fmt.Errorf("%w: binary body %d exceeds %d", ErrTooLarge, bodyLen, maxBody)
	}
	if uint64(extrasLen+keyLen) > bodyLen {
		return nil, fmt.Errorf("%w: extras %d + key %d exceeds body %d",
			ErrMalformedRequest, extrasLen, keyLen, bodyLen)
	}

	body := make([]byte, int(bodyLen))
	if _, err := io.ReadFull(s.r, body); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("%w: incomplete binary body", ErrTruncated)
		}
		return nil, err
	}

	opcode := header[1]
	op, ok := binaryOps[opcode]
	if !ok {
		return nil, fmt.Errorf("%w: binary opcode %#x", ErrUnsupportedCommand, opcode)
	}
	if extrasLen != op.extras {
		return nil, fmt.Errorf("%w: %s expects %d extras bytes, got %d",
			ErrMalformedRequest, op.name, op.extras, extrasLen)
	}
	if op.keyRequired && keyLen == 0 {
		return nil, fmt.Errorf("%w: %s requires a key", ErrMalformedRequest, op.name)
	}
	if !op.keyAllowed && keyLen != 0 {
		return nil, fmt.Errorf("%w: %s does not take a key", ErrMalformedRequest, op.name)
	}

	valueLen := int(bodyLen) - extrasLen - keyLen
	if !op.valueAllowed && valueLen != 0 {
		return nil, fmt.Errorf("%w: %s does not take a value", ErrMalformedRequest, op.name)
	}
	if op.valueAllowed && valueLen > s.n.cfg.MaxValueLength {
		return nil, fmt.Errorf("%w: binary value %d exceeds %d",
			ErrTooLarge, valueLen, s.n.cfg.MaxValueLength)
	}

	request := &Request{
		Protocol:     ProtocolBinary,
		Command:      op.name,
		NoReply:      op.response == ResponseNone,
		CloseAfter:   op.closeAfter,
		ResponseKind: op.response,
		Opaque:       binary.BigEndian.Uint32(header[12:16]),
		BinaryOpcode: opcode,
	}

	var keyBytes []byte
	if keyLen != 0 {
		keyBytes = body[extrasLen : extrasLen+keyLen]
		prefixed, err := s.n.PrefixKey(keyBytes)
		if err != nil {
			return nil, err
		}
		request.Keys = [][]byte{copyBytes(keyBytes)}
		newBody := make([]byte, 0, len(body)+len(prefixed)-keyLen)
		newBody = append(newBody, body[:extrasLen]...)
		newBody = append(newBody, prefixed...)
		newBody = append(newBody, body[extrasLen+keyLen:]...)
		binary.BigEndian.PutUint16(header[2:4], uint16(len(prefixed)))
		binary.BigEndian.PutUint32(header[8:12], uint32(len(newBody)))
		body = newBody
	}
	if op.valueAllowed && valueLen != 0 {
		start := len(body) - valueLen
		request.Value = body[start:]
	}

	out := make([]byte, 0, binaryHeaderLength+len(body))
	out = append(out, header...)
	out = append(out, body...)
	request.Bytes = out
	return request, nil
}

// consumeBinaryFrame consumes one bounded binary frame without normalizing it.
// It is used to keep the stream position deterministic when binary forwarding
// is disabled, so the protocol lock applies to subsequent bytes.
func (s *Session) consumeBinaryFrame() error {
	header := make([]byte, binaryHeaderLength)
	if _, err := io.ReadFull(s.r, header); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return fmt.Errorf("%w: incomplete binary header", ErrTruncated)
		}
		return err
	}
	if header[0] != binaryRequestMagic {
		return fmt.Errorf("%w: binary request magic is %#x", ErrMalformedRequest, header[0])
	}
	bodyLen := uint64(binary.BigEndian.Uint32(header[8:12]))
	maxBody := uint64(s.n.cfg.MaxValueLength) + MaxProtocolKeyLength + 64
	if bodyLen > maxBody {
		return fmt.Errorf("%w: binary body %d exceeds %d", ErrTooLarge, bodyLen, maxBody)
	}
	if _, err := io.CopyN(io.Discard, s.r, int64(bodyLen)); err != nil {
		if err == io.EOF {
			return fmt.Errorf("%w: incomplete binary body", ErrTruncated)
		}
		return err
	}
	return nil
}

func (n *Normalizer) readBinaryResponse(r io.Reader, req *Request) ([]byte, error) {
	header := make([]byte, binaryHeaderLength)
	if _, err := io.ReadFull(r, header); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("%w: incomplete binary response header", ErrTruncated)
		}
		return nil, err
	}
	if header[0] != binaryResponseMagic {
		return nil, fmt.Errorf("%w: response magic is %#x", ErrMalformedResponse, header[0])
	}
	keyLen := int(binary.BigEndian.Uint16(header[2:4]))
	extrasLen := int(header[4])
	bodyLen := uint64(binary.BigEndian.Uint32(header[8:12]))
	maxBody := uint64(n.cfg.MaxResponseLength)
	if bodyLen > maxBody {
		return nil, fmt.Errorf("%w: binary response body %d exceeds %d",
			ErrTooLarge, bodyLen, maxBody)
	}
	if bodyLen+binaryHeaderLength > uint64(n.cfg.MaxResponseLength) {
		return nil, fmt.Errorf("%w: binary response exceeds %d bytes",
			ErrTooLarge, n.cfg.MaxResponseLength)
	}
	if uint64(extrasLen+keyLen) > bodyLen {
		return nil, fmt.Errorf("%w: binary response extras/key exceed body", ErrMalformedResponse)
	}
	body := make([]byte, int(bodyLen))
	if _, err := io.ReadFull(r, body); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("%w: incomplete binary response body", ErrTruncated)
		}
		return nil, err
	}

	if keyLen == 0 {
		out := make([]byte, 0, binaryHeaderLength+len(body))
		out = append(out, header...)
		out = append(out, body...)
		return out, nil
	}

	key := body[extrasLen : extrasLen+keyLen]
	stripped, tenant := n.StripKey(key)
	if !tenant {
		// Consume the frame completely and emit nothing: the value belongs to
		// another tenant.
		return nil, nil
	}
	newBody := make([]byte, 0, len(body)-keyLen+len(stripped))
	newBody = append(newBody, body[:extrasLen]...)
	newBody = append(newBody, stripped...)
	newBody = append(newBody, body[extrasLen+keyLen:]...)
	binary.BigEndian.PutUint16(header[2:4], uint16(len(stripped)))
	binary.BigEndian.PutUint32(header[8:12], uint32(len(newBody)))
	out := make([]byte, 0, binaryHeaderLength+len(newBody))
	out = append(out, header...)
	out = append(out, newBody...)
	_ = req
	return out, nil
}
