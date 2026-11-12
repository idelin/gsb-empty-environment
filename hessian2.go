// Package netsec provides stream-level preflight checks for inbound RPC
// payloads before they are handed to a Hessian2 deserializer.
//
// The checker walks the Hessian2 grammar without materializing the object
// graph. It enforces a caller supplied class whitelist, validates every value
// reference and applies resource budgets for nesting depth, the number of
// class instances and the declared length of lists and maps.
package netsec

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// Sentinel errors returned by Inspect. Callers can use errors.Is to classify a
// rejection while the wrapped message keeps the offending byte or index.
var (
	ErrUnknownType     = errors.New("hessian2: unknown type byte")
	ErrClassNotAllowed = errors.New("hessian2: class name is not in whitelist")
	ErrBadReference    = errors.New("hessian2: reference points to an object that has not appeared")
	ErrDepthExceeded   = errors.New("hessian2: nesting depth budget exceeded")
	ErrObjectLimit     = errors.New("hessian2: total object budget exceeded")
	ErrLengthExceeded  = errors.New("hessian2: declared list/map length budget exceeded")
	ErrMalformedStream = errors.New("hessian2: truncated or malformed stream")
	ErrInvalidLength   = errors.New("hessian2: negative declared length")
	ErrClassIndex      = errors.New("hessian2: object references an undefined class definition")
)

// Default resource budgets applied when a Policy leaves a limit at zero.
const (
	DefaultMaxDepth     = 64
	DefaultMaxObjects   = 65536
	DefaultMaxContainer = 1 << 20
)

// Policy configures a preflight inspection. Whitelist is the set of fully
// qualified class names a Hessian2 class definition (the C production) is
// allowed to carry. A nil or empty whitelist rejects every class definition,
// so object-bearing streams fail closed.
type Policy struct {
	Whitelist    map[string]struct{}
	MaxDepth     int
	MaxObjects   int
	MaxContainer int
}

// DefaultPolicy returns a policy with the built-in resource budgets and the
// supplied whitelist.
func DefaultPolicy(whitelist map[string]struct{}) Policy {
	return Policy{
		Whitelist:    whitelist,
		MaxDepth:     DefaultMaxDepth,
		MaxObjects:   DefaultMaxObjects,
		MaxContainer: DefaultMaxContainer,
	}
}

type classDef struct {
	name       string
	fieldCount int
}

type checker struct {
	r      *bufio.Reader
	policy Policy

	classes []classDef
	// objects is the running number of referenceable values (class instances,
	// lists and maps). A Q reference must stay below this number.
	objects int
	// instances counts class instances only and backs the MaxObjects budget.
	instances int
	depth     int
	// held keeps a type byte consumed during lookahead, e.g. before a typed
	// map type string.
	held     bool
	heldByte byte
}

// Inspect performs a Hessian2 preflight check on r using policy. A nil error
// means the stream is structurally valid according to the policy; any other
// error wraps one of the package's sentinel errors.
func Inspect(r io.Reader, policy Policy) error {
	if policy.MaxDepth <= 0 {
		policy.MaxDepth = DefaultMaxDepth
	}
	if policy.MaxObjects <= 0 {
		policy.MaxObjects = DefaultMaxObjects
	}
	if policy.MaxContainer <= 0 {
		policy.MaxContainer = DefaultMaxContainer
	}

	c := &checker{
		r:      bufio.NewReader(r),
		policy: policy,
	}

	// A Hessian2 body is a sequence of top-level values; the stream must end
	// cleanly so trailing attacker controlled bytes are rejected.
	for {
		if err := c.parseValue(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (c *checker) readByte() (byte, error) {
	if c.held {
		c.held = false
		return c.heldByte, nil
	}
	b, err := c.r.ReadByte()
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, errors.Join(ErrMalformedStream, err)
	}
	return b, err
}

func (c *checker) unread(b byte) {
	c.held = true
	c.heldByte = b
}

func (c *checker) readN(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return nil, errors.Join(ErrMalformedStream, err)
	}
	return buf, nil
}

func (c *checker) readUint16() (int, error) {
	buf, err := c.readN(2)
	if err != nil {
		return 0, err
	}
	return int(buf[0])<<8 | int(buf[1]), nil
}

func (c *checker) readUint24() (int, error) {
	buf, err := c.readN(3)
	if err != nil {
		return 0, err
	}
	return int(buf[0])<<16 | int(buf[1])<<8 | int(buf[2]), nil
}

// readInt decodes a Hessian2 integer production and returns its signed value.
// The compact integer grammar is:
//
//	int ::= [0x80-0xbf]                 direct integer -16..47
//	int ::= [0xc0-0xcf] b0              two-octet integer
//	int ::= [0xd0-0xd7] b1 b0           three-octet integer
//	int ::= 'I' b3 b2 b1 b0             four-octet integer
func (c *checker) readInt(tag byte) (int, error) {
	switch {
	case tag >= 0x80 && tag <= 0xbf:
		return int(int8(tag - 0x90)), nil
	case tag >= 0xc0 && tag <= 0xcf:
		next, err := c.readByte()
		if err != nil {
			return 0, err
		}
		return int(int16(uint16(tag-0xc8)<<8 | uint16(next))), nil
	case tag >= 0xd0 && tag <= 0xd7:
		buf, err := c.readN(2)
		if err != nil {
			return 0, err
		}
		v := int32(tag-0xd4)<<16 | int32(buf[0])<<8 | int32(buf[1])
		return int(int32(v)), nil
	case tag == 0x49: // 'I'
		buf, err := c.readN(4)
		if err != nil {
			return 0, err
		}
		return int(int32(buf[0])<<24 | int32(buf[1])<<16 | int32(buf[2])<<8 | int32(buf[3])), nil
	}
	return 0, errors.Join(ErrUnknownType, errors.New("hessian2: expected integer tag"))
}

// skipLong consumes the payload of the compact long productions:
//
//	long ::= [0xd8-0xef] b0              two-octet long
//	long ::= [0xf0-0xf7] b1 b0           three-octet long
//	long ::= [0xf8-0xff]                 direct long
func (c *checker) skipLong(tag byte) error {
	switch {
	case tag >= 0xd8 && tag <= 0xef:
		_, err := c.readN(1)
		return err
	case tag >= 0xf0 && tag <= 0xf7:
		_, err := c.readN(2)
		return err
	case tag >= 0xf8 && tag <= 0xff:
		return nil
	}
	return errors.Join(ErrUnknownType, errors.New("hessian2: expected long tag"))
}

// readString decodes a string production, reassembling chunked strings.
func (c *checker) readString(tag byte) (string, error) {
	var out bytes.Buffer
	for {
		var length int

		switch {
		case tag >= 0x00 && tag <= 0x1f:
			length = int(tag)
		case tag >= 0x30 && tag <= 0x33:
			length = int(tag-0x30) << 8
			next, err := c.readByte()
			if err != nil {
				return "", err
			}
			length |= int(next)
		case tag == 0x53: // 'S' final chunk, 16-bit length
			n, err := c.readUint16()
			if err != nil {
				return "", err
			}
			length = n
		case tag == 0x52: // 'R' non-final chunk, 16-bit length
			n, err := c.readUint16()
			if err != nil {
				return "", err
			}
			length = n
		default:
			return "", errors.Join(ErrUnknownType, errors.New("hessian2: expected string tag"))
		}

		buf, err := c.readN(length)
		if err != nil {
			return "", err
		}
		out.Write(buf)

		if tag != 0x52 {
			break
		}
		next, err := c.readByte()
		if err != nil {
			return "", err
		}
		tag = next
	}
	return out.String(), nil
}

// skipBinary walks a binary production without retaining the payload.
func (c *checker) skipBinary(tag byte) error {
	for {
		var length int
		final := true
		switch {
		case tag >= 0x20 && tag <= 0x2f:
			length = int(tag - 0x20)
		case tag >= 0x34 && tag <= 0x37:
			length = int(tag-0x34) << 8
			next, err := c.readByte()
			if err != nil {
				return err
			}
			length |= int(next)
		case tag == 0x42: // 'B' non-final chunk
			final = false
			n, err := c.readUint16()
			if err != nil {
				return err
			}
			length = n
		case tag == 0x62: // 'b' final chunk
			n, err := c.readUint16()
			if err != nil {
				return err
			}
			length = n
		default:
			return errors.Join(ErrUnknownType, errors.New("hessian2: expected binary tag"))
		}
		if _, err := c.readN(length); err != nil {
			return err
		}
		if final {
			return nil
		}
		next, err := c.readByte()
		if err != nil {
			return err
		}
		tag = next
	}
}

func (c *checker) checkLength(length int) error {
	if length < 0 {
		return errors.Join(ErrInvalidLength, errors.New("hessian2: negative length"))
	}
	if length > c.policy.MaxContainer {
		return errors.Join(ErrLengthExceeded, errors.New("hessian2: declared length exceeds budget"))
	}
	return nil
}

// parseClass handles the C class-definition production.
func (c *checker) parseClass() error {
	tag, err := c.readByte()
	if err != nil {
		return err
	}
	name, err := c.readString(tag)
	if err != nil {
		return err
	}

	fieldTag, err := c.readByte()
	if err != nil {
		return err
	}
	fieldCount, err := c.readInt(fieldTag)
	if err != nil {
		return err
	}
	if fieldCount < 0 {
		return errors.Join(ErrMalformedStream, errors.New("hessian2: negative class field count"))
	}
	if err := c.checkLength(fieldCount); err != nil {
		return err
	}

	if _, ok := c.policy.Whitelist[name]; !ok {
		return errors.Join(ErrClassNotAllowed, errors.New("hessian2: class "+name+" rejected"))
	}

	for i := 0; i < fieldCount; i++ {
		nameTag, err := c.readByte()
		if err != nil {
			return err
		}
		if _, err := c.readString(nameTag); err != nil {
			return err
		}
	}

	c.classes = append(c.classes, classDef{name: name, fieldCount: fieldCount})
	return nil
}

// parseObject handles O (named object) and the compact object bytes 0x60-0x6f.
func (c *checker) parseObject(tag byte) error {
	classIndex := -1
	if tag == 0x4f { // 'O'
		idxTag, err := c.readByte()
		if err != nil {
			return err
		}
		idx, err := c.readInt(idxTag)
		if err != nil {
			return err
		}
		classIndex = idx
	} else {
		// Compact object 0x60-0x6f encodes the class definition index.
		classIndex = int(tag - 0x60)
	}

	if classIndex < 0 || classIndex >= len(c.classes) {
		return errors.Join(ErrClassIndex, errors.New("hessian2: undefined class index"))
	}
	if c.instances >= c.policy.MaxObjects {
		return ErrObjectLimit
	}

	// The instance becomes referenceable before its fields are parsed, so a
	// back reference forming a graph cycle is structurally legal.
	c.objects++
	c.instances++

	c.depth++
	if c.depth > c.policy.MaxDepth {
		return ErrDepthExceeded
	}
	defer func() { c.depth-- }()

	for i := 0; i < c.classes[classIndex].fieldCount; i++ {
		if err := c.parseValue(); err != nil {
			return err
		}
	}
	return nil
}

// parseReference handles the Q (0x51) reference production.
func (c *checker) parseReference() error {
	index, err := c.readUint24()
	if err != nil {
		return err
	}
	if index >= c.objects {
		return errors.Join(ErrBadReference, errors.New("hessian2: reference index out of range"))
	}
	return nil
}

// parseOptionalType consumes the type string that prefixes typed variable
// containers when the next byte is a string production.
func (c *checker) parseOptionalType() error {
	tag, err := c.readByte()
	if err != nil {
		return err
	}
	if tag <= 0x1f || (tag >= 0x30 && tag <= 0x33) || tag == 0x52 || tag == 0x53 {
		_, err = c.readString(tag)
		return err
	}
	c.unread(tag)
	return nil
}

func (c *checker) parseList(tag byte) error {
	length := -1

	switch {
	case tag >= 0x40 && tag <= 0x47: // compact untyped fixed-length list (length 0-7)
		length = int(tag - 0x40)
	case tag == 0x56: // 'V' variable-length list
		if err := c.parseOptionalType(); err != nil {
			return err
		}
	case tag == 0x55: // 'U' untyped variable-length list
	case tag == 0x57: // 'W' fixed-length untyped list
		lenTag, err := c.readByte()
		if err != nil {
			return err
		}
		length, err = c.readInt(lenTag)
		if err != nil {
			return err
		}
	case tag == 0x58: // 'X' fixed-length typed list
		if err := c.parseOptionalType(); err != nil {
			return err
		}
		lenTag, err := c.readByte()
		if err != nil {
			return err
		}
		length, err = c.readInt(lenTag)
		if err != nil {
			return err
		}
	default:
		return errors.Join(ErrUnknownType, errors.New("hessian2: expected list tag"))
	}

	if length >= 0 {
		if err := c.checkLength(length); err != nil {
			return err
		}
	}

	if err := c.enterContainer(); err != nil {
		return err
	}
	defer c.leaveContainer()

	if length >= 0 {
		for i := 0; i < length; i++ {
			if err := c.parseValue(); err != nil {
				return err
			}
		}
		return nil
	}

	// Variable-length lists and maps terminate with the 'z' end byte.
	elements := 0
	for {
		next, err := c.readByte()
		if err != nil {
			return err
		}
		if next == 0x7a { // 'z'
			return nil
		}
		c.unread(next)
		elements++
		if elements > c.policy.MaxContainer {
			return errors.Join(ErrLengthExceeded, errors.New("hessian2: list element count exceeds budget"))
		}
		if err := c.parseValue(); err != nil {
			return err
		}
	}
}

func (c *checker) parseMap(tag byte) error {
	if tag == 0x48 { // 'H' typed map: type string precedes the entries
		typeTag, err := c.readByte()
		if err != nil {
			return err
		}
		if _, err := c.readString(typeTag); err != nil {
			return err
		}
	}

	if err := c.enterContainer(); err != nil {
		return err
	}
	defer c.leaveContainer()

	entries := 0
	for {
		next, err := c.readByte()
		if err != nil {
			return err
		}
		if next == 0x7a { // 'z'
			return nil
		}
		c.unread(next)

		entries++
		if entries > c.policy.MaxContainer {
			return errors.Join(ErrLengthExceeded, errors.New("hessian2: map entry count exceeds budget"))
		}
		if err := c.parseValue(); err != nil {
			return err
		}
		if err := c.parseValue(); err != nil {
			return err
		}
	}
}

func (c *checker) enterContainer() error {
	c.depth++
	if c.depth > c.policy.MaxDepth {
		return ErrDepthExceeded
	}
	c.objects++
	return nil
}

func (c *checker) leaveContainer() {
	c.depth--
}

// parseValue walks a single Hessian2 value. Any byte outside the recognized
// grammar stops the walk immediately and the whole stream is rejected.
func (c *checker) parseValue() error {
	tag, err := c.readByte()
	if err != nil {
		return err
	}

	switch {
	// Compact final string 0x00-0x1f; two-octet compact string 0x30-0x33.
	case tag <= 0x1f || (tag >= 0x30 && tag <= 0x33):
		_, err = c.readString(tag)
		return err

	// Compact final binary 0x20-0x2f; two-octet compact binary 0x34-0x37.
	case (tag >= 0x20 && tag <= 0x2f) || (tag >= 0x34 && tag <= 0x37):
		return c.skipBinary(tag)

	// Compact untyped fixed-length lists 0x40-0x47. The named productions
	// B/C/F/H inside this range are matched by their own cases below.
	case tag == 0x40, tag == 0x41, tag == 0x45, tag == 0x47:
		return c.parseList(tag)

	// 0x38-0x3f is reserved and must not appear in a valid Hessian2 stream.
	case tag >= 0x38 && tag <= 0x3f:
		return errors.Join(ErrUnknownType, errors.New("hessian2: reserved type byte"))

	// Named containers and chunked productions.
	case tag == 0x42, tag == 0x62: // 'B'/'b' chunked binary
		return c.skipBinary(tag)
	case tag == 0x43: // 'C' class definition
		return c.parseClass()
	case tag == 0x4a: // 'J' 64-bit date, millisecond precision
		_, err = c.readN(8)
		return err
	case tag == 0x46: // 'F' boolean false
		return nil
	case tag == 0x48: // 'H' typed map
		return c.parseMap(tag)
	case tag == 0x49: // 'I' 32-bit integer
		_, err = c.readInt(tag)
		return err
	case tag == 0x4b: // 'K' 32-bit date, minute precision
		_, err = c.readN(4)
		return err
	case tag == 0x4c: // 'L' 64-bit long
		_, err = c.readN(8)
		return err
	case tag == 0x4d: // 'M' untyped map
		return c.parseMap(tag)
	case tag == 0x4e: // 'N' null
		return nil
	case tag == 0x4f: // 'O' named object
		return c.parseObject(tag)
	case tag == 0x51: // 'Q' value reference
		return c.parseReference()
	case tag == 0x52, tag == 0x53: // 'R'/'S' chunked string
		_, err = c.readString(tag)
		return err
	case tag == 0x54: // 'T' boolean true
		return nil

	// Compact doubles 0x5b-0x5f per the Hessian2 double grammar.
	case tag == 0x5b: // 0.0
		return nil
	case tag == 0x5c: // 1.0
		return nil
	case tag == 0x5d: // single-byte double
		_, err = c.readN(1)
		return err
	case tag == 0x5e: // two-byte double
		_, err = c.readN(2)
		return err
	case tag == 0x5f: // eight-byte double
		_, err = c.readN(8)
		return err

	// Variable and fixed-length long-form lists.
	case tag == 0x55 || tag == 0x56 || tag == 0x57 || tag == 0x58:
		return c.parseList(tag)

	// Compact object 0x60-0x6f.
	case tag >= 0x60 && tag <= 0x6f:
		return c.parseObject(tag)

	// Compact integers 0x80-0xd7 and compact longs 0xd8-0xff.
	case tag >= 0x80 && tag <= 0xd7:
		_, err = c.readInt(tag)
		return err
	case tag >= 0xd8:
		return c.skipLong(tag)

	// End marker outside a container is a grammar violation.
	case tag == 0x7a: // 'z'
		return errors.Join(ErrMalformedStream, errors.New("hessian2: unexpected end marker"))

	default:
		return errors.Join(ErrUnknownType, errors.New("hessian2: unrecognized type byte"))
	}
}
