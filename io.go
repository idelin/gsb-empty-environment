package archiveproof

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// DecodeArchive reads one JSON archive object.
func DecodeArchive(r io.Reader) (*Archive, error) {
	var a Archive
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return nil, fmt.Errorf("archiveproof: decode archive: %w", err)
	}
	return &a, nil
}

// DecodeCertificate reads one JSON certificate object.
func DecodeCertificate(r io.Reader) (*Certificate, error) {
	var c Certificate
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("archiveproof: decode certificate: %w", err)
	}
	return &c, nil
}

// DecodeClocking reads one JSON source-to-offset map.
func DecodeClocking(r io.Reader) (Clocking, error) {
	var c Clocking
	dec := json.NewDecoder(r)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("archiveproof: decode clocking: %w", err)
	}
	return c, nil
}

// EncodeJSON writes v as indented JSON.
func EncodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// LoadArchive reads and decodes an archive file.
func LoadArchive(path string) (*Archive, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return DecodeArchive(f)
}

// LoadCertificate reads and decodes a certificate file.
func LoadCertificate(path string) (*Certificate, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return DecodeCertificate(f)
}

// LoadClocking reads and decodes a clocking file.
func LoadClocking(path string) (Clocking, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return DecodeClocking(f)
}
