package netsec

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
)

const (
	recordTypeHandshake        uint8  = 22
	recordTypeAlert            uint8  = 21
	handshakeClientHello       uint8  = 1
	handshakeServerHello       uint8  = 2
	extensionSupportedVersions uint16 = 43
	extensionFallbackMarker    uint16 = 0xfe00
)

const fallbackMarkerLen = 5 // extension type, length, and one data byte

var errNotClientHello = errors.New("netsec: record is not a ClientHello")
var errNotServerHello = errors.New("netsec: record is not a ServerHello")

type clientHelloParts struct {
	legacyVersion     uint16
	cipherSuites      []uint16
	cipherLenOff      int
	cipherDataOff     int
	handshakeLenOff   int
	recordLenOff      int
	extensionsLenOff  int
	extensionsDataOff int
	extensionsEnd     int
}

func parseClientHelloRecord(record []byte) (*clientHelloParts, error) {
	if len(record) < 5+4+2+32+1 {
		return nil, errNotClientHello
	}
	if record[0] != recordTypeHandshake {
		return nil, errNotClientHello
	}
	recordLen := int(binary.BigEndian.Uint16(record[3:5]))
	if recordLen+5 != len(record) {
		return nil, errNotClientHello
	}
	if record[5] != handshakeClientHello {
		return nil, errNotClientHello
	}
	hsLen := int(record[6])<<16 | int(record[7])<<8 | int(record[8])
	if hsLen+9 > len(record) {
		return nil, errNotClientHello
	}

	body := 9
	legacy := binary.BigEndian.Uint16(record[body : body+2])
	p := body + 2 + 32
	sessionIDLen := int(record[p])
	p++
	if p+sessionIDLen+2 > len(record) {
		return nil, errNotClientHello
	}
	p += sessionIDLen
	cipherLenOff := p
	cipherLen := int(binary.BigEndian.Uint16(record[p : p+2]))
	p += 2
	if cipherLen%2 != 0 || p+cipherLen > len(record) {
		return nil, errNotClientHello
	}
	cipherDataOff := p
	p += cipherLen
	if p >= len(record) || p+1+int(record[p]) > len(record) {
		return nil, errNotClientHello
	}
	compressionLen := int(record[p])
	p++
	p += compressionLen
	extensionsLenOff := -1
	extensionsDataOff := -1
	extensionsEnd := -1
	if p < len(record) {
		if p+2 > len(record) {
			return nil, errNotClientHello
		}
		extensionsLenOff = p
		extLen := int(binary.BigEndian.Uint16(record[p : p+2]))
		p += 2
		if p+extLen > len(record) {
			return nil, errNotClientHello
		}
		extensionsDataOff = p
		extensionsEnd = p + extLen
	}

	suites := make([]uint16, 0, cipherLen/2)
	for off := cipherDataOff; off < cipherDataOff+cipherLen; off += 2 {
		suites = append(suites, binary.BigEndian.Uint16(record[off:off+2]))
	}

	return &clientHelloParts{
		legacyVersion:     legacy,
		cipherSuites:      suites,
		cipherLenOff:      cipherLenOff,
		cipherDataOff:     cipherDataOff,
		handshakeLenOff:   6,
		recordLenOff:      3,
		extensionsLenOff:  extensionsLenOff,
		extensionsDataOff: extensionsDataOff,
		extensionsEnd:     extensionsEnd,
	}, nil
}

func addSCSVToClientHelloRecord(record []byte) ([]byte, error) {
	parts, err := parseClientHelloRecord(record)
	if err != nil {
		return nil, err
	}
	if HasFallbackSCSV(parts.cipherSuites) {
		return record, nil
	}
	insertAt := parts.cipherDataOff + len(parts.cipherSuites)*2
	if parts.extensionsLenOff < 0 || parts.extensionsEnd != len(record) {
		out := make([]byte, len(record)+2)
		copy(out, record[:insertAt])
		binary.BigEndian.PutUint16(out[insertAt:insertAt+2], TLSFallbackSCSV)
		copy(out[insertAt+2:], record[insertAt:])
		increaseLengths(out, 2, parts.handshakeLenOff, parts.recordLenOff, parts.cipherLenOff)
		return out, nil
	}

	out := make([]byte, len(record)+2+fallbackMarkerLen)
	copy(out, record[:insertAt])
	binary.BigEndian.PutUint16(out[insertAt:insertAt+2], TLSFallbackSCSV)
	copy(out[insertAt+2:], record[insertAt:parts.extensionsEnd])
	markerAt := parts.extensionsEnd + 2
	binary.BigEndian.PutUint16(out[markerAt:markerAt+2], extensionFallbackMarker)
	binary.BigEndian.PutUint16(out[markerAt+2:markerAt+4], 1)
	out[markerAt+4] = 1
	copy(out[markerAt+fallbackMarkerLen:], record[parts.extensionsEnd:])

	increaseLengths(out, 2, parts.handshakeLenOff, parts.recordLenOff, parts.cipherLenOff)
	increaseLengthsNoCipher(out, fallbackMarkerLen, parts.handshakeLenOff, parts.recordLenOff)
	increaseUint16(out, parts.extensionsLenOff+2, fallbackMarkerLen)
	return out, nil
}

func findExtension(record []byte, parts *clientHelloParts, extType uint16) (int, bool) {
	if parts.extensionsDataOff < 0 {
		return 0, false
	}
	for p := parts.extensionsDataOff; p+4 <= parts.extensionsEnd; {
		typ := binary.BigEndian.Uint16(record[p : p+2])
		length := int(binary.BigEndian.Uint16(record[p+2 : p+4]))
		if p+4+length > parts.extensionsEnd {
			return 0, false
		}
		if typ == extType {
			return p, true
		}
		p += 4 + length
	}
	return 0, false
}

func increaseUint16(record []byte, off, delta int) {
	value := int(binary.BigEndian.Uint16(record[off : off+2]))
	binary.BigEndian.PutUint16(record[off:off+2], uint16(value+delta))
}

func removeFallbackMarkerAndSCSV(record []byte) ([]byte, error) {
	parts, err := parseClientHelloRecord(record)
	if err != nil {
		return nil, err
	}
	markerAt, hasMarker := findExtension(record, parts, extensionFallbackMarker)
	if !hasMarker {
		// Leave ClientHellos from other implementations untouched. A marker is
		// only added by the wire-level retry path, where the client transcript
		// was computed before TLS_FALLBACK_SCSV was inserted.
		return record, nil
	}

	var scsvAt = -1
	for i, suite := range parts.cipherSuites {
		if suite == TLSFallbackSCSV {
			scsvAt = parts.cipherDataOff + i*2
			break
		}
	}
	if scsvAt < 0 {
		out := make([]byte, len(record)-fallbackMarkerLen)
		copy(out, record[:markerAt])
		copy(out[markerAt:], record[markerAt+fallbackMarkerLen:])
		increaseLengthsNoCipher(out, -fallbackMarkerLen, parts.handshakeLenOff, parts.recordLenOff)
		increaseUint16(out, parts.extensionsLenOff, -fallbackMarkerLen)
		return out, nil
	}

	out := make([]byte, len(record)-2-fallbackMarkerLen)
	copy(out, record[:scsvAt])
	copy(out[scsvAt:], record[scsvAt+2:markerAt])
	copy(out[markerAt-2:], record[markerAt+fallbackMarkerLen:])
	increaseLengthsNoCipher(out, -2-fallbackMarkerLen, parts.handshakeLenOff, parts.recordLenOff)
	increaseUint16(out, parts.extensionsLenOff-2, -fallbackMarkerLen)
	increaseUint16(out, parts.cipherLenOff, -2)
	return out, nil
}

func removeSCSVFromClientHelloRecord(record []byte) ([]byte, error) {
	return removeFallbackMarkerAndSCSV(record)
}

func increaseLengths(record []byte, delta int, handshakeLenOff, recordLenOff, cipherLenOff int) {
	if delta == 0 {
		return
	}
	hsLen := int(record[handshakeLenOff])<<16 | int(record[handshakeLenOff+1])<<8 | int(record[handshakeLenOff+2])
	hsLen += delta
	record[handshakeLenOff] = byte(hsLen >> 16)
	record[handshakeLenOff+1] = byte(hsLen >> 8)
	record[handshakeLenOff+2] = byte(hsLen)

	recLen := int(binary.BigEndian.Uint16(record[recordLenOff : recordLenOff+2]))
	recLen += delta
	binary.BigEndian.PutUint16(record[recordLenOff:recordLenOff+2], uint16(recLen))

	cipherLen := int(binary.BigEndian.Uint16(record[cipherLenOff : cipherLenOff+2]))
	cipherLen += delta
	binary.BigEndian.PutUint16(record[cipherLenOff:cipherLenOff+2], uint16(cipherLen))
}

func increaseLengthsNoCipher(record []byte, delta int, handshakeLenOff, recordLenOff int) {
	if delta == 0 {
		return
	}
	hsLen := int(record[handshakeLenOff])<<16 | int(record[handshakeLenOff+1])<<8 | int(record[handshakeLenOff+2])
	hsLen += delta
	record[handshakeLenOff] = byte(hsLen >> 16)
	record[handshakeLenOff+1] = byte(hsLen >> 8)
	record[handshakeLenOff+2] = byte(hsLen)

	recLen := int(binary.BigEndian.Uint16(record[recordLenOff : recordLenOff+2]))
	recLen += delta
	binary.BigEndian.PutUint16(record[recordLenOff:recordLenOff+2], uint16(recLen))
}

type serverHelloParts struct {
	legacyVersion     uint16
	negotiatedVersion uint16
	random            []byte
}

func parseServerHelloRecord(record []byte) (*serverHelloParts, error) {
	if len(record) < 5+4+2+32+1 {
		return nil, errNotServerHello
	}
	if record[0] != recordTypeHandshake || record[5] != handshakeServerHello {
		return nil, errNotServerHello
	}
	recordLen := int(binary.BigEndian.Uint16(record[3:5]))
	if recordLen+5 != len(record) {
		return nil, errNotServerHello
	}
	hsLen := int(record[6])<<16 | int(record[7])<<8 | int(record[8])
	if hsLen+9 > len(record) {
		return nil, errNotServerHello
	}

	body := 9
	legacy := binary.BigEndian.Uint16(record[body : body+2])
	random := append([]byte(nil), record[body+2:body+2+32]...)
	p := body + 2 + 32
	sessionIDLen := int(record[p])
	p++
	if p+sessionIDLen+2+1 > len(record) {
		return nil, errNotServerHello
	}
	p += sessionIDLen
	p += 2 // cipher_suite
	p++    // compression_method
	if p >= len(record) {
		return &serverHelloParts{legacyVersion: legacy, negotiatedVersion: legacy, random: random}, nil
	}
	extLen := int(binary.BigEndian.Uint16(record[p : p+2]))
	p += 2
	end := p + extLen
	if end > len(record) {
		return nil, errNotServerHello
	}
	negotiated := legacy
	for p+4 <= end {
		extType := binary.BigEndian.Uint16(record[p : p+2])
		extDataLen := int(binary.BigEndian.Uint16(record[p+2 : p+4]))
		p += 4
		if p+extDataLen > end {
			return nil, errNotServerHello
		}
		if extType == extensionSupportedVersions && extDataLen >= 2 {
			negotiated = binary.BigEndian.Uint16(record[p : p+2])
		}
		p += extDataLen
	}
	return &serverHelloParts{legacyVersion: legacy, negotiatedVersion: negotiated, random: random}, nil
}

func readTLSRecord(conn net.Conn) ([]byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint16(header[3:5]))
	record := make([]byte, 5+length)
	copy(record, header)
	if _, err := io.ReadFull(conn, record[5:]); err != nil {
		return nil, err
	}
	return record, nil
}

func sendTLSAlert(conn net.Conn, alert uint8) error {
	record := []byte{recordTypeAlert, 3, 3, 0, 2, 2, alert}
	_, err := conn.Write(record)
	return err
}
