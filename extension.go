package netsec

import (
	"encoding/binary"
	"fmt"
)

// TLS extension and handshake message types used by certificate compression.
const (
	ExtensionTypeCompressCertificate uint16 = 27

	// ExtensionTypeCertificateCompressionDictionary is the private-use
	// extension defined by this package for advertising dictionary IDs.
	//
	// Its payload is a TLS-style vector of uint16 dictionary IDs:
	//
	//   uint16 length;
	//   uint16 dictionary_ids[length / 2];
	//
	// The extension itself never carries dictionary bytes. The server looks up
	// the bytes through its tenant-scoped DictionaryRegistry.
	ExtensionTypeCertificateCompressionDictionary uint16 = 0xff01
)

const (
	HandshakeTypeCertificate           uint8 = 11
	HandshakeTypeCompressedCertificate uint8 = 25
	maxAlgorithmVectorLength                 = 254
)

// ParseCompressCertificateExtension parses the body of a client
// compress_certificate(27) extension:
//
//	uint8 length;
//	uint16 algorithms[length / 2];
//
// Unknown identifiers are preserved; selection skips algorithms for which no
// codec is installed.
func ParseCompressCertificateExtension(data []byte) ([]Algorithm, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("%w: missing algorithm vector length", ErrInvalidExtension)
	}
	length := int(data[0])
	if length < 2 || length > maxAlgorithmVectorLength || length%2 != 0 || len(data) != 1+length {
		return nil, fmt.Errorf("%w: algorithm vector length %d with payload %d", ErrInvalidExtension, length, len(data)-1)
	}

	algorithms := make([]Algorithm, 0, length/2)
	for offset := 1; offset < len(data); offset += 2 {
		algorithms = append(algorithms, Algorithm(binary.BigEndian.Uint16(data[offset:offset+2])))
	}
	return algorithms, nil
}

// MarshalCompressCertificateExtension encodes a list of algorithms as the body
// of a compress_certificate(27) extension.
func MarshalCompressCertificateExtension(algorithms []Algorithm) ([]byte, error) {
	if len(algorithms) == 0 {
		return nil, fmt.Errorf("%w: empty algorithm list", ErrInvalidExtension)
	}
	if len(algorithms) > maxAlgorithmVectorLength/2 {
		return nil, fmt.Errorf("%w: too many algorithms", ErrInvalidExtension)
	}

	data := make([]byte, 1+len(algorithms)*2)
	data[0] = byte(len(algorithms) * 2)
	for index, algorithm := range algorithms {
		binary.BigEndian.PutUint16(data[1+index*2:1+index*2+2], uint16(algorithm))
	}
	return data, nil
}

// ParseCertificateCompressionDictionaryExtension parses the dictionary ID
// vector used by the private dictionary extension.
func ParseCertificateCompressionDictionaryExtension(data []byte) ([]DictionaryID, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("%w: dictionary extension too short", ErrInvalidExtension)
	}
	length := int(binary.BigEndian.Uint16(data[:2]))
	if length%2 != 0 || len(data) != 2+length {
		return nil, fmt.Errorf("%w: dictionary vector length %d", ErrInvalidExtension, length)
	}

	ids := make([]DictionaryID, 0, length/2)
	for offset := 2; offset < len(data); offset += 2 {
		id := DictionaryID(binary.BigEndian.Uint16(data[offset : offset+2]))
		if id == NoDictionary {
			return nil, fmt.Errorf("%w: dictionary ID 0 is reserved", ErrInvalidExtension)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// MarshalCertificateCompressionDictionaryExtension encodes a dictionary ID
// vector for the private dictionary extension.
func MarshalCertificateCompressionDictionaryExtension(ids []DictionaryID) ([]byte, error) {
	if len(ids) > 0xffff/2 {
		return nil, fmt.Errorf("%w: too many dictionary IDs", ErrInvalidExtension)
	}
	data := make([]byte, 2+len(ids)*2)
	binary.BigEndian.PutUint16(data[:2], uint16(len(ids)*2))
	for index, id := range ids {
		if id == NoDictionary {
			return nil, fmt.Errorf("%w: dictionary ID 0 is reserved", ErrInvalidExtension)
		}
		binary.BigEndian.PutUint16(data[2+index*2:2+index*2+2], uint16(id))
	}
	return data, nil
}
