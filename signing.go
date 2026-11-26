package apkverify

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const (
	apkSigningBlockMagic = "APK Sig Block 42"
	signingBlockOverhead = 8 + 8 + 16

	schemeV2  Scheme = 0x7109871a
	schemeV3  Scheme = 0xf05368c0
	schemeV31 Scheme = 0x1b93ad61

	proofOfRotationAttrID = 0x3ba06f8c
)

// Scheme identifies an APK signature scheme block.
type Scheme uint32

const (
	// SchemeV2 is APK Signature Scheme v2.
	SchemeV2 Scheme = schemeV2
	// SchemeV3 is APK Signature Scheme v3.
	SchemeV3 Scheme = schemeV3
	// SchemeV31 is APK Signature Scheme v3.1.
	SchemeV31 Scheme = schemeV31
)

func (s Scheme) String() string {
	switch s {
	case SchemeV2:
		return "v2"
	case SchemeV3:
		return "v3"
	case SchemeV31:
		return "v3.1"
	default:
		return fmt.Sprintf("0x%08x", uint32(s))
	}
}

// Digest is one content digest declared by a signer.
type Digest struct {
	AlgorithmID uint32
	Digest      []byte
}

// SignatureRecord is one signature declared by a signer.
type SignatureRecord struct {
	AlgorithmID uint32
	Signature   []byte
}

// Signer is a structurally parsed signer from a v2, v3, or v3.1 block.
type Signer struct {
	SignedData      []byte
	MinSDKVersion   uint32
	MaxSDKVersion   uint32
	HasSDKRange     bool
	Certificates    [][]byte
	Digests         []Digest
	Signatures      []SignatureRecord
	PublicKey       []byte
	Lineage         *Lineage
	AdditionalAttrs []Attribute
}

// Attribute is an additional signed or unsigned attribute.
type Attribute struct {
	ID    uint32
	Value []byte
}

// Lineage is a parsed proof-of-rotation declaration.
type Lineage struct {
	Version      uint32
	Certificates [][]byte
	Nodes        int
}

// SchemeBlock is one ID-value pair containing a recognized signature scheme.
type SchemeBlock struct {
	Scheme  Scheme
	Value   []byte
	Signers []Signer
}

// SigningBlock is an APK Signing Block immediately before the central
// directory.
type SigningBlock struct {
	Start   int64
	Size    uint64
	Schemes []SchemeBlock
}

// Scheme returns the recognized scheme block with the requested ID.
func (b *SigningBlock) Scheme(scheme Scheme) (*SchemeBlock, bool) {
	if b == nil {
		return nil, false
	}
	for i := range b.Schemes {
		if b.Schemes[i].Scheme == scheme {
			return &b.Schemes[i], true
		}
	}
	return nil, false
}

// HasV2OrNewer reports whether at least one structurally valid v2, v3, or
// v3.1 signer block is present.
func (b *SigningBlock) HasV2OrNewer() bool {
	if b == nil {
		return false
	}
	for _, block := range b.Schemes {
		if len(block.Signers) > 0 {
			return true
		}
	}
	return false
}

func parseSigningBlock(data []byte, cdOffset int) (*SigningBlock, int, error) {
	if cdOffset < len(apkSigningBlockMagic) {
		return nil, -1, nil
	}
	if !bytes.Equal(data[cdOffset-len(apkSigningBlockMagic):cdOffset], []byte(apkSigningBlockMagic)) {
		return nil, -1, nil
	}
	if cdOffset < signingBlockOverhead {
		return nil, -1, fmt.Errorf("%w: truncated footer", ErrSigningBlock)
	}

	size := binary.LittleEndian.Uint64(data[cdOffset-24 : cdOffset-16])
	if size < signingBlockOverhead-8 {
		return nil, -1, fmt.Errorf("%w: size %d is too small", ErrSigningBlock, size)
	}
	if size > uint64(cdOffset) {
		return nil, -1, fmt.Errorf("%w: size %d exceeds buffer", ErrSigningBlock, size)
	}
	start := cdOffset - 8 - int(size)
	if start < 0 || start+8 > cdOffset-24 {
		return nil, -1, fmt.Errorf("%w: block boundaries are invalid", ErrSigningBlock)
	}
	if frontSize := binary.LittleEndian.Uint64(data[start : start+8]); frontSize != size {
		return nil, -1, fmt.Errorf("%w: front size %d differs from footer size %d",
			ErrSigningBlock, frontSize, size)
	}

	pairsStart := start + 8
	pairsEnd := cdOffset - 24
	block, err := parseSigningPairs(data[pairsStart:pairsEnd], start, size)
	if err != nil {
		return nil, -1, err
	}
	return block, start, nil
}

func parseSigningPairs(data []byte, blockStart int, blockSize uint64) (*SigningBlock, error) {
	block := &SigningBlock{
		Start: int64(blockStart),
		Size:  blockSize,
	}
	seen := make(map[uint32]struct{})
	offset := 0
	for offset < len(data) {
		if len(data)-offset < 8 {
			return nil, fmt.Errorf("%w: truncated ID-value pair length", ErrSigningBlock)
		}
		pairSize := binary.LittleEndian.Uint64(data[offset : offset+8])
		offset += 8
		if pairSize < 4 {
			return nil, fmt.Errorf("%w: ID-value pair is shorter than its ID", ErrSigningBlock)
		}
		if pairSize > uint64(len(data)-offset) {
			return nil, fmt.Errorf("%w: ID-value pair exceeds block", ErrSigningBlock)
		}
		pairEnd := offset + int(pairSize)
		id := binary.LittleEndian.Uint32(data[offset : offset+4])
		value := data[offset+4 : pairEnd]
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("%w: duplicate ID-value pair 0x%08x", ErrSigningBlock, id)
		}
		seen[id] = struct{}{}

		switch Scheme(id) {
		case SchemeV2, SchemeV3, SchemeV31:
			signers, err := parseSignerSequence(value, Scheme(id))
			if err != nil {
				return nil, fmt.Errorf("%w: %s block: %v", ErrSigningBlock, Scheme(id), err)
			}
			if len(signers) == 0 {
				return nil, fmt.Errorf("%w: %s block has no signers", ErrSigningBlock, Scheme(id))
			}
			block.Schemes = append(block.Schemes, SchemeBlock{
				Scheme:  Scheme(id),
				Value:   value,
				Signers: signers,
			})
		}
		offset = pairEnd
	}
	if offset != len(data) {
		return nil, fmt.Errorf("%w: ID-value sequence is not aligned", ErrSigningBlock)
	}
	return block, nil
}

func parseSignerSequence(data []byte, scheme Scheme) ([]Signer, error) {
	signersBytes, rest, err := readLengthPrefixed(data)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("trailing bytes after signer sequence")
	}
	if len(signersBytes) == 0 {
		return nil, fmt.Errorf("empty signer sequence")
	}

	var signers []Signer
	offset := 0
	for offset < len(signersBytes) {
		record, next, err := readLengthPrefixed(signersBytes[offset:])
		if err != nil {
			return nil, err
		}
		if len(record) == 0 {
			return nil, fmt.Errorf("empty signer record")
		}
		signer, err := parseSigner(record, scheme)
		if err != nil {
			return nil, err
		}
		signers = append(signers, signer)
		offset = len(signersBytes) - len(next)
	}
	return signers, nil
}

func parseSigner(data []byte, scheme Scheme) (Signer, error) {
	signedData, rest, err := readLengthPrefixed(data)
	if err != nil {
		return Signer{}, fmt.Errorf("signed data: %w", err)
	}
	signatures, rest, err := readLengthPrefixed(rest)
	if err != nil {
		return Signer{}, fmt.Errorf("signatures: %w", err)
	}
	publicKey, rest, err := readLengthPrefixed(rest)
	if err != nil {
		return Signer{}, fmt.Errorf("public key: %w", err)
	}
	if len(rest) != 0 {
		return Signer{}, fmt.Errorf("trailing signer bytes")
	}
	if len(publicKey) == 0 {
		return Signer{}, fmt.Errorf("empty public key")
	}

	digests, certs, attrs, minSDK, maxSDK, hasRange, err := parseSignedData(signedData, scheme)
	if err != nil {
		return Signer{}, err
	}
	sigRecords, err := parseSignatureRecords(signatures)
	if err != nil {
		return Signer{}, err
	}
	if len(digests) == 0 || len(sigRecords) == 0 || len(certs) == 0 {
		return Signer{}, fmt.Errorf("signer lacks digest, signature, or certificate")
	}
	if err := matchSignatureAlgorithms(digests, sigRecords); err != nil {
		return Signer{}, err
	}

	signer := Signer{
		SignedData:      signedData,
		MinSDKVersion:   minSDK,
		MaxSDKVersion:   maxSDK,
		HasSDKRange:     hasRange,
		Certificates:    certs,
		Digests:         digests,
		Signatures:      sigRecords,
		PublicKey:       publicKey,
		AdditionalAttrs: attrs,
	}
	for _, attr := range attrs {
		if attr.ID != proofOfRotationAttrID {
			continue
		}
		lineage, err := parseLineage(attr.Value)
		if err != nil {
			return Signer{}, fmt.Errorf("proof-of-rotation: %w", err)
		}
		signer.Lineage = lineage
	}
	return signer, nil
}

func parseSignedData(data []byte, scheme Scheme) ([]Digest, [][]byte, []Attribute, uint32, uint32, bool, error) {
	digestBytes, rest, err := readLengthPrefixed(data)
	if err != nil {
		return nil, nil, nil, 0, 0, false, fmt.Errorf("digests: %w", err)
	}
	certBytes, rest, err := readLengthPrefixed(rest)
	if err != nil {
		return nil, nil, nil, 0, 0, false, fmt.Errorf("certificates: %w", err)
	}

	var minSDK, maxSDK uint32
	var hasRange bool
	if scheme == SchemeV3 || scheme == SchemeV31 {
		if len(rest) < 8 {
			return nil, nil, nil, 0, 0, false, fmt.Errorf("v3 SDK range is truncated")
		}
		minSDK = binary.LittleEndian.Uint32(rest[:4])
		maxSDK = binary.LittleEndian.Uint32(rest[4:8])
		rest = rest[8:]
		if minSDK > maxSDK {
			return nil, nil, nil, 0, 0, false, fmt.Errorf("v3 minSdkVersion %d exceeds maxSdkVersion %d", minSDK, maxSDK)
		}
		hasRange = true
	}

	attrBytes, rest, err := readLengthPrefixed(rest)
	if err != nil {
		return nil, nil, nil, 0, 0, false, fmt.Errorf("attributes: %w", err)
	}
	if len(rest) != 0 {
		return nil, nil, nil, 0, 0, false, fmt.Errorf("trailing signed data")
	}

	digests, err := parseDigestRecords(digestBytes)
	if err != nil {
		return nil, nil, nil, 0, 0, false, err
	}
	certs, err := parseLengthPrefixedItems(certBytes, true)
	if err != nil {
		return nil, nil, nil, 0, 0, false, fmt.Errorf("certificates: %w", err)
	}
	attrs, err := parseAttributes(attrBytes)
	if err != nil {
		return nil, nil, nil, 0, 0, false, err
	}
	return digests, certs, attrs, minSDK, maxSDK, hasRange, nil
}

func parseDigestRecords(data []byte) ([]Digest, error) {
	records, err := parseLengthPrefixedItems(data, false)
	if err != nil {
		return nil, fmt.Errorf("digests: %w", err)
	}
	digests := make([]Digest, 0, len(records))
	for _, record := range records {
		if len(record) < 4 {
			return nil, fmt.Errorf("digest record is truncated")
		}
		algorithm := binary.LittleEndian.Uint32(record[:4])
		digest, rest, err := readLengthPrefixed(record[4:])
		if err != nil {
			return nil, fmt.Errorf("digest value: %w", err)
		}
		if len(rest) != 0 || len(digest) == 0 {
			return nil, fmt.Errorf("digest record is malformed")
		}
		digests = append(digests, Digest{AlgorithmID: algorithm, Digest: digest})
	}
	return digests, nil
}

func parseSignatureRecords(data []byte) ([]SignatureRecord, error) {
	records, err := parseLengthPrefixedItems(data, false)
	if err != nil {
		return nil, fmt.Errorf("signatures: %w", err)
	}
	signatures := make([]SignatureRecord, 0, len(records))
	for _, record := range records {
		if len(record) < 4 {
			return nil, fmt.Errorf("signature record is truncated")
		}
		algorithm := binary.LittleEndian.Uint32(record[:4])
		signature, rest, err := readLengthPrefixed(record[4:])
		if err != nil {
			return nil, fmt.Errorf("signature value: %w", err)
		}
		if len(rest) != 0 || len(signature) == 0 {
			return nil, fmt.Errorf("signature record is malformed")
		}
		signatures = append(signatures, SignatureRecord{AlgorithmID: algorithm, Signature: signature})
	}
	return signatures, nil
}

func matchSignatureAlgorithms(digests []Digest, signatures []SignatureRecord) error {
	digestAlgorithms := make(map[uint32]struct{}, len(digests))
	for _, digest := range digests {
		if _, duplicate := digestAlgorithms[digest.AlgorithmID]; duplicate {
			return fmt.Errorf("duplicate digest algorithm 0x%08x", digest.AlgorithmID)
		}
		digestAlgorithms[digest.AlgorithmID] = struct{}{}
	}
	seen := make(map[uint32]struct{}, len(signatures))
	for _, signature := range signatures {
		if _, ok := digestAlgorithms[signature.AlgorithmID]; !ok {
			return fmt.Errorf("signature algorithm 0x%08x has no matching digest", signature.AlgorithmID)
		}
		if _, duplicate := seen[signature.AlgorithmID]; duplicate {
			return fmt.Errorf("duplicate signature algorithm 0x%08x", signature.AlgorithmID)
		}
		seen[signature.AlgorithmID] = struct{}{}
	}
	for algorithm := range digestAlgorithms {
		if _, ok := seen[algorithm]; !ok {
			return fmt.Errorf("digest algorithm 0x%08x has no matching signature", algorithm)
		}
	}
	return nil
}

func parseAttributes(data []byte) ([]Attribute, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var attrs []Attribute
	seen := make(map[uint32]struct{})
	offset := 0
	for offset < len(data) {
		if len(data)-offset < 4 {
			return nil, fmt.Errorf("truncated additional attribute ID")
		}
		id := binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4
		value, next, err := readLengthPrefixed(data[offset:])
		if err != nil {
			return nil, fmt.Errorf("additional attribute 0x%08x: %w", id, err)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("duplicate additional attribute 0x%08x", id)
		}
		seen[id] = struct{}{}
		attrs = append(attrs, Attribute{ID: id, Value: value})
		offset = len(data) - len(next)
	}
	return attrs, nil
}

func parseLineage(data []byte) (*Lineage, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("truncated lineage")
	}
	version := binary.LittleEndian.Uint32(data[:4])
	nodesBytes, rest, err := readLengthPrefixed(data[4:])
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("trailing lineage bytes")
	}
	lineage := &Lineage{Version: version}
	offset := 0
	for offset < len(nodesBytes) {
		node, next, err := readLengthPrefixed(nodesBytes[offset:])
		if err != nil {
			return nil, fmt.Errorf("lineage node: %w", err)
		}
		signedData, nodeRest, err := readLengthPrefixed(node)
		if err != nil {
			return nil, fmt.Errorf("lineage signed data: %w", err)
		}
		signature, afterSignature, err := readLengthPrefixed(nodeRest)
		if err != nil {
			return nil, fmt.Errorf("lineage signature: %w", err)
		} else if len(afterSignature) != 0 {
			return nil, fmt.Errorf("trailing lineage node bytes")
		}
		if len(signature) == 0 {
			return nil, fmt.Errorf("empty lineage signature")
		}
		certificate, signedRest, err := readLengthPrefixed(signedData)
		if err != nil {
			return nil, fmt.Errorf("lineage certificate: %w", err)
		}
		if len(certificate) == 0 || len(signedRest) != 8 {
			return nil, fmt.Errorf("lineage node signed data is malformed")
		}
		lineage.Certificates = append(lineage.Certificates, certificate)
		lineage.Nodes++
		offset = len(nodesBytes) - len(next)
	}
	if lineage.Nodes == 0 {
		return nil, fmt.Errorf("empty lineage")
	}
	return lineage, nil
}

func parseLengthPrefixedItems(sequence []byte, requireNonEmptyItems bool) ([][]byte, error) {
	var items [][]byte
	offset := 0
	for offset < len(sequence) {
		item, next, err := readLengthPrefixed(sequence[offset:])
		if err != nil {
			return nil, err
		}
		if requireNonEmptyItems && len(item) == 0 {
			return nil, fmt.Errorf("empty sequence item")
		}
		items = append(items, item)
		offset = len(sequence) - len(next)
	}
	return items, nil
}

func readLengthPrefixed(data []byte) ([]byte, []byte, error) {
	if len(data) < 4 {
		return nil, nil, fmt.Errorf("truncated length prefix")
	}
	size := uint64(binary.LittleEndian.Uint32(data[:4]))
	if size > uint64(len(data)-4) {
		return nil, nil, fmt.Errorf("length %d exceeds remaining %d bytes", size, len(data)-4)
	}
	return data[4 : 4+int(size)], data[4+int(size):], nil
}
