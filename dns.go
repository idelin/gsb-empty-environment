package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	dnsHeaderLen = 12

	optType = 41

	flagQR = 0x8000
	flagTC = 0x0200
	flagRD = 0x0100
	flagCD = 0x0010
	flagDO = 0x8000

	opcodeMask     = 0x7800
	extRCodeBad    = BadCookieRCode >> 4
	baseRCodeBad   = BadCookieRCode & 0x0f
	defaultUDPSize = 512
	serverUDPSize  = 1232
)

// ErrMalformedDNS indicates that a message could not be parsed as DNS wire
// data. CookieGate.Inspect and CookieGate.Check return it without attempting to
// generate a response.
var ErrMalformedDNS = errors.New("dns cookie: malformed DNS message")

type parsedMessage struct {
	flags     uint16
	qdCount   uint16
	qEnd      int
	hasOPT    bool
	udpSize   uint16
	do        bool
	extRCode  uint8
	hasCookie bool
	client    []byte
	server    []byte
}

func parseMessage(message []byte) (parsedMessage, error) {
	var parsed parsedMessage
	if len(message) < dnsHeaderLen {
		return parsed, fmt.Errorf("%w: header is %d bytes", ErrMalformedDNS, len(message))
	}
	parsed.flags = binary.BigEndian.Uint16(message[2:4])
	parsed.qdCount = binary.BigEndian.Uint16(message[4:6])
	anCount := binary.BigEndian.Uint16(message[6:8])
	nsCount := binary.BigEndian.Uint16(message[8:10])
	arCount := binary.BigEndian.Uint16(message[10:12])

	offset := dnsHeaderLen
	for i := uint16(0); i < parsed.qdCount; i++ {
		next, err := skipName(message, offset)
		if err != nil {
			return parsed, err
		}
		offset = next
		if offset+4 > len(message) {
			return parsed, fmt.Errorf("%w: truncated question", ErrMalformedDNS)
		}
		offset += 4
	}
	parsed.qEnd = offset

	for section := 0; section < 3; section++ {
		var count uint16
		switch section {
		case 0:
			count = anCount
		case 1:
			count = nsCount
		default:
			count = arCount
		}
		for i := uint16(0); i < count; i++ {
			nameOffset := offset
			next, err := skipName(message, offset)
			if err != nil {
				return parsed, err
			}
			offset = next
			if offset+10 > len(message) {
				return parsed, fmt.Errorf("%w: truncated resource record", ErrMalformedDNS)
			}
			recordType := binary.BigEndian.Uint16(message[offset : offset+2])
			recordClass := binary.BigEndian.Uint16(message[offset+2 : offset+4])
			recordTTL := binary.BigEndian.Uint32(message[offset+4 : offset+8])
			rdLength := int(binary.BigEndian.Uint16(message[offset+8 : offset+10]))
			offset += 10
			if rdLength > len(message)-offset {
				return parsed, fmt.Errorf("%w: truncated RDATA", ErrMalformedDNS)
			}
			rdata := message[offset : offset+rdLength]
			offset += rdLength

			if section != 2 || recordType != optType {
				continue
			}
			if parsed.hasOPT {
				return parsed, fmt.Errorf("%w: multiple OPT records", ErrMalformedDNS)
			}
			if next != nameOffset+1 || message[nameOffset] != 0 {
				return parsed, fmt.Errorf("%w: OPT owner name is not root", ErrMalformedDNS)
			}
			parsed.hasOPT = true
			parsed.udpSize = recordClass
			parsed.extRCode = uint8(recordTTL >> 24)
			parsed.do = recordTTL&flagDO != 0
			if err := parsed.parseOptions(rdata); err != nil {
				return parsed, err
			}
		}
	}
	if offset != len(message) {
		return parsed, fmt.Errorf("%w: %d trailing bytes", ErrMalformedDNS, len(message)-offset)
	}
	return parsed, nil
}

func (parsed *parsedMessage) parseOptions(rdata []byte) error {
	for offset := 0; offset < len(rdata); {
		if len(rdata)-offset < 4 {
			return fmt.Errorf("%w: truncated EDNS option", ErrMalformedDNS)
		}
		code := binary.BigEndian.Uint16(rdata[offset : offset+2])
		length := int(binary.BigEndian.Uint16(rdata[offset+2 : offset+4]))
		offset += 4
		if length > len(rdata)-offset {
			return fmt.Errorf("%w: truncated EDNS option data", ErrMalformedDNS)
		}
		data := rdata[offset : offset+length]
		offset += length
		if code != CookieOptionCode {
			continue
		}
		if parsed.hasCookie {
			return fmt.Errorf("%w: multiple Cookie options", ErrMalformedDNS)
		}
		parsed.hasCookie = true
		if len(data) >= ClientCookieLen {
			parsed.client = append([]byte(nil), data[:ClientCookieLen]...)
			parsed.server = append([]byte(nil), data[ClientCookieLen:]...)
		} else {
			parsed.client = append([]byte(nil), data...)
		}
	}
	return nil
}

func skipName(message []byte, offset int) (int, error) {
	if offset >= len(message) {
		return 0, fmt.Errorf("%w: name starts beyond message", ErrMalformedDNS)
	}
	next := -1
	for labels := 0; labels < 128; labels++ {
		if offset >= len(message) {
			return 0, fmt.Errorf("%w: truncated name", ErrMalformedDNS)
		}
		length := message[offset]
		switch length & 0xc0 {
		case 0x00:
			if length == 0 {
				if next < 0 {
					next = offset + 1
				}
				return next, nil
			}
			offset++
			if int(length) > len(message)-offset {
				return 0, fmt.Errorf("%w: truncated label", ErrMalformedDNS)
			}
			offset += int(length)
		case 0xc0:
			if offset+2 > len(message) {
				return 0, fmt.Errorf("%w: truncated compression pointer", ErrMalformedDNS)
			}
			if next < 0 {
				next = offset + 2
			}
			pointer := int(binary.BigEndian.Uint16(message[offset:offset+2]) & 0x3fff)
			if pointer >= len(message) {
				return 0, fmt.Errorf("%w: compression pointer out of range", ErrMalformedDNS)
			}
			offset = pointer
		default:
			return 0, fmt.Errorf("%w: invalid label type", ErrMalformedDNS)
		}
	}
	return 0, fmt.Errorf("%w: compression loop", ErrMalformedDNS)
}

func buildTCResponse(query []byte, parsed parsedMessage, requestLen int) []byte {
	question := query[dnsHeaderLen:parsed.qEnd]
	response := make([]byte, dnsHeaderLen+len(question))
	copy(response[:2], query[:2])
	flags := parsed.flags&opcodeMask | parsed.flags&(flagRD|flagCD) | flagQR | flagTC
	binary.BigEndian.PutUint16(response[2:4], flags)
	binary.BigEndian.PutUint16(response[4:6], parsed.qdCount)
	copy(response[dnsHeaderLen:], question)

	// A valid Cookie query always has an additional section, so copying the
	// question section should leave the response smaller than the request.
	// Keep the header-only form as a belt-and-braces amplification guard.
	if len(response) > requestLen {
		response = response[:dnsHeaderLen]
		binary.BigEndian.PutUint16(response[4:6], 0)
	}
	return response
}

func buildBadCookieResponse(query []byte, parsed parsedMessage, clientCookie, serverCookie []byte) []byte {
	question := query[dnsHeaderLen:parsed.qEnd]
	response := make([]byte, dnsHeaderLen, dnsHeaderLen+len(question)+11+28)
	copy(response[:2], query[:2])
	flags := parsed.flags&opcodeMask | parsed.flags&(flagRD|flagCD) | flagQR | baseRCodeBad
	binary.BigEndian.PutUint16(response[2:4], flags)
	binary.BigEndian.PutUint16(response[4:6], parsed.qdCount)
	binary.BigEndian.PutUint16(response[10:12], 1)
	response = append(response, question...)

	udpSize := parsed.udpSize
	if !parsed.hasOPT || udpSize < defaultUDPSize {
		udpSize = serverUDPSize
	}
	var optFlags uint16
	if parsed.do {
		optFlags = flagDO
	}

	rdLength := 0
	if len(clientCookie) == ClientCookieLen && len(serverCookie) == ServerCookieLen {
		rdLength = 4 + ClientCookieLen + ServerCookieLen
	}
	response = append(response, 0)
	response = append(response, byte(optType>>8), byte(optType))
	response = append(response, byte(udpSize>>8), byte(udpSize))
	response = append(response, byte(extRCodeBad), 0, byte(optFlags>>8), byte(optFlags))
	response = append(response, byte(rdLength>>8), byte(rdLength))
	if rdLength != 0 {
		response = append(response,
			byte(CookieOptionCode>>8), byte(CookieOptionCode),
			byte((ClientCookieLen+ServerCookieLen)>>8), byte(ClientCookieLen+ServerCookieLen),
		)
		response = append(response, clientCookie...)
		response = append(response, serverCookie...)
	}
	return response
}
