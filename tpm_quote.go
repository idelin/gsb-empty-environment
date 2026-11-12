package netsec

type parsedQuote struct {
	nonce     []byte
	selection []PCRBank
	pcrDigest []byte
}

// parseQuote decodes a TPM2B_ATTEST carrying TPMS_ATTEST for TPM2_Quote.
func parseQuote(raw []byte) (parsedQuote, error) {
	outer := newByteReader(raw)
	body, err := outer.readSized16()
	if err != nil {
		return parsedQuote{}, ErrMalformedInput
	}
	if outer.remaining() != 0 {
		return parsedQuote{}, ErrMalformedInput
	}

	r := newByteReader(body)
	magic, err := r.readU16()
	if err != nil {
		return parsedQuote{}, ErrMalformedInput
	}
	attestType, err := r.readU16()
	if err != nil {
		return parsedQuote{}, ErrMalformedInput
	}
	if magic != tpmGeneratedMagic || attestType != tpmStAttestQuote {
		return parsedQuote{}, ErrMalformedInput
	}
	if _, err := r.readSized16(); err != nil { // qualifiedSigner
		return parsedQuote{}, ErrMalformedInput
	}
	nonce, err := r.readSized16()
	if err != nil {
		return parsedQuote{}, ErrMalformedInput
	}
	if _, err := r.read(8); err != nil { // clock
		return parsedQuote{}, ErrMalformedInput
	}
	if _, err := r.read(4); err != nil { // resetCount
		return parsedQuote{}, ErrMalformedInput
	}
	if _, err := r.read(4); err != nil { // restartCount
		return parsedQuote{}, ErrMalformedInput
	}
	if _, err := r.read(1); err != nil { // safe
		return parsedQuote{}, ErrMalformedInput
	}
	if _, err := r.read(8); err != nil { // firmwareVersion
		return parsedQuote{}, ErrMalformedInput
	}

	selection, err := parsePCRSelection(r)
	if err != nil {
		return parsedQuote{}, err
	}
	digest, err := r.readSized16()
	if err != nil {
		return parsedQuote{}, ErrMalformedInput
	}
	if r.remaining() != 0 {
		return parsedQuote{}, ErrMalformedInput
	}

	return parsedQuote{
		nonce:     append([]byte{}, nonce...),
		selection: selection,
		pcrDigest: append([]byte{}, digest...),
	}, nil
}

// parsePCRSelection decodes TPML_PCR_SELECTION at the current reader position.
func parsePCRSelection(r *byteReader) ([]PCRBank, error) {
	count, err := r.readU32()
	if err != nil {
		return nil, ErrMalformedInput
	}
	if count == 0 || count > 16 {
		return nil, ErrMalformedInput
	}
	banks := make([]PCRBank, 0, count)
	seen := map[AlgorithmID]bool{}
	for i := uint32(0); i < count; i++ {
		hashAlg, err := r.readU16()
		if err != nil {
			return nil, ErrMalformedInput
		}
		if _, err := hashSpecFor(AlgorithmID(hashAlg)); err != nil {
			return nil, err
		}
		if seen[AlgorithmID(hashAlg)] {
			return nil, ErrMalformedInput
		}
		seen[AlgorithmID(hashAlg)] = true

		sizeOfSelect, err := r.readU8()
		if err != nil {
			return nil, ErrMalformedInput
		}
		if sizeOfSelect == 0 || sizeOfSelect > 4 {
			return nil, ErrMalformedInput
		}
		bitmap, err := r.read(int(sizeOfSelect))
		if err != nil {
			return nil, ErrMalformedInput
		}
		pcrs := expandPCRBitmap(bitmap)
		if len(pcrs) == 0 {
			return nil, ErrMalformedInput
		}
		banks = append(banks, PCRBank{Hash: AlgorithmID(hashAlg), PCRs: pcrs})
	}
	return banks, nil
}

func expandPCRBitmap(bitmap []byte) []int {
	var pcrs []int
	for i, b := range bitmap {
		for bit := 0; bit < 8; bit++ {
			if b&(1<<(7-bit)) != 0 {
				pcrs = append(pcrs, i*8+bit)
			}
		}
	}
	return pcrs
}
