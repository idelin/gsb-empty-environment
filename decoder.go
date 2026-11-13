package netsec

import "fmt"

const (
	scopeTx         = "Tx"
	scopeTxBody     = "TxBody"
	scopeAny        = "Any"
	scopeAuthInfo   = "AuthInfo"
	scopeSignerInfo = "SignerInfo"
	scopeModeInfo   = "ModeInfo"
	scopeSingle     = "ModeInfo.Single"
	scopeMulti      = "ModeInfo.Multi"
	scopeBitArray   = "BitArray"
	scopeFee        = "Fee"
	scopeCoin       = "Coin"
	scopeTip        = "Tip"
)

// ParseTx decodes protobuf transaction bytes without applying admission
// policy. Unknown fields are retained on the decoded messages so callers can
// inspect them or pass the transaction to Policy.Validate.
func ParseTx(raw []byte) (*Tx, error) {
	fields, err := parseFields(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedTx, err)
	}

	tx := &Tx{Raw: raw}
	for _, field := range fields {
		switch field.Number {
		case 1:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			tx.Body, err = parseTxBody(field.Bytes, "tx.body")
			if err != nil {
				return nil, err
			}
		case 2:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			tx.AuthInfo, err = parseAuthInfo(field.Bytes, "tx.auth_info")
			if err != nil {
				return nil, err
			}
		case 3:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			tx.Signatures = append(tx.Signatures, cloneBytes(field.Bytes))
		default:
			tx.UnknownFields = append(tx.UnknownFields, newUnknownField(field, scopeTx, "tx"))
		}
	}
	return tx, nil
}

// DecodeTx decodes and applies admission checks. With no options it uses a
// fail-closed policy: no Amino message registry is configured and no legacy
// Amino height gate is active. Use ParseTx for pure decoding, or options such
// as WithPolicy, WithAminoRegistry, WithHeight, and WithSigners to supply the
// caller context.
func DecodeTx(raw []byte, opts ...DecodeOption) (*Tx, error) {
	config := decodeConfig{}
	for _, option := range opts {
		if option != nil {
			option(&config)
		}
	}
	tx, err := ParseTx(raw)
	if err != nil {
		return nil, err
	}
	tx.Height = config.request.Height
	tx.Signers = append([]string(nil), config.request.Signers...)
	if err := config.policy.Validate(tx, config.request); err != nil {
		return nil, err
	}
	return tx, nil
}

func parseTxBody(data []byte, path string) (*TxBody, error) {
	fields, err := parseFields(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformedTx, path, err)
	}
	body := &TxBody{}
	for _, field := range fields {
		switch field.Number {
		case 1:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			msg, err := parseAny(field.Bytes, fmt.Sprintf("%s.messages[%d]", path, len(body.Messages)))
			if err != nil {
				return nil, err
			}
			body.Messages = append(body.Messages, msg)
		case 2:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			body.Memo = string(field.Bytes)
		case 3:
			if err := requireWire(field, WireVarint); err != nil {
				return nil, err
			}
			body.TimeoutHeight = field.Varint
		case 1023:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			opt, err := parseAny(field.Bytes, fmt.Sprintf("%s.extension_options[%d]", path, len(body.ExtensionOptions)))
			if err != nil {
				return nil, err
			}
			body.ExtensionOptions = append(body.ExtensionOptions, opt)
		case 2047:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			opt, err := parseAny(field.Bytes, fmt.Sprintf(
				"%s.non_critical_extension_options[%d]",
				path,
				len(body.NonCriticalExtensionOptions),
			))
			if err != nil {
				return nil, err
			}
			body.NonCriticalExtensionOptions = append(body.NonCriticalExtensionOptions, opt)
		default:
			body.UnknownFields = append(body.UnknownFields, newUnknownField(field, scopeTxBody, path))
		}
	}
	return body, nil
}

func parseAny(data []byte, path string) (*Any, error) {
	fields, err := parseFields(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformedTx, path, err)
	}
	value := &Any{}
	for _, field := range fields {
		switch field.Number {
		case 1:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			value.TypeURL = string(field.Bytes)
		case 2:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			value.Value = cloneBytes(field.Bytes)
		default:
			value.UnknownFields = append(value.UnknownFields, newUnknownField(field, scopeAny, path))
		}
	}
	return value, nil
}

func parseAuthInfo(data []byte, path string) (*AuthInfo, error) {
	fields, err := parseFields(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformedTx, path, err)
	}
	auth := &AuthInfo{}
	for _, field := range fields {
		switch field.Number {
		case 1:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			si, err := parseSignerInfo(field.Bytes, fmt.Sprintf("%s.signer_infos[%d]", path, len(auth.SignerInfos)))
			if err != nil {
				return nil, err
			}
			auth.SignerInfos = append(auth.SignerInfos, si)
		case 2:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			auth.Fee, err = parseFee(field.Bytes, path+".fee")
			if err != nil {
				return nil, err
			}
		case 3:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			auth.Tip, err = parseTip(field.Bytes, path+".tip")
			if err != nil {
				return nil, err
			}
		default:
			auth.UnknownFields = append(auth.UnknownFields, newUnknownField(field, scopeAuthInfo, path))
		}
	}
	return auth, nil
}

func parseSignerInfo(data []byte, path string) (*SignerInfo, error) {
	fields, err := parseFields(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformedTx, path, err)
	}
	info := &SignerInfo{}
	for _, field := range fields {
		switch field.Number {
		case 1:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			info.PublicKey, err = parseAny(field.Bytes, path+".public_key")
			if err != nil {
				return nil, err
			}
		case 2:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			info.ModeInfo, err = parseModeInfo(field.Bytes, path+".mode_info")
			if err != nil {
				return nil, err
			}
		case 3:
			if err := requireWire(field, WireVarint); err != nil {
				return nil, err
			}
			info.Sequence = field.Varint
		default:
			info.UnknownFields = append(info.UnknownFields, newUnknownField(field, scopeSignerInfo, path))
		}
	}
	return info, nil
}

func parseModeInfo(data []byte, path string) (*ModeInfo, error) {
	fields, err := parseFields(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformedTx, path, err)
	}
	info := &ModeInfo{}
	for _, field := range fields {
		switch field.Number {
		case 1:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			single, err := parseSingle(field.Bytes, path+".single")
			if err != nil {
				return nil, err
			}
			info.Single = single
		case 2:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			multi, err := parseMulti(field.Bytes, path+".multi")
			if err != nil {
				return nil, err
			}
			info.Multi = multi
		default:
			info.UnknownFields = append(info.UnknownFields, newUnknownField(field, scopeModeInfo, path))
		}
	}
	return info, nil
}

func parseSingle(data []byte, path string) (*Single, error) {
	fields, err := parseFields(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformedTx, path, err)
	}
	single := &Single{}
	for _, field := range fields {
		switch field.Number {
		case 1:
			if err := requireWire(field, WireVarint); err != nil {
				return nil, err
			}
			single.Mode = SignMode(int32(field.Varint))
		default:
			single.UnknownFields = append(single.UnknownFields, newUnknownField(field, scopeSingle, path))
		}
	}
	return single, nil
}

func parseMulti(data []byte, path string) (*Multi, error) {
	fields, err := parseFields(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformedTx, path, err)
	}
	multi := &Multi{}
	for _, field := range fields {
		switch field.Number {
		case 1:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			bits, err := parseBitArray(field.Bytes, path+".bitarray")
			if err != nil {
				return nil, err
			}
			multi.BitArray = bits
		case 2:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			nested, err := parseModeInfo(field.Bytes, fmt.Sprintf("%s.mode_infos[%d]", path, len(multi.ModeInfos)))
			if err != nil {
				return nil, err
			}
			multi.ModeInfos = append(multi.ModeInfos, nested)
		default:
			multi.UnknownFields = append(multi.UnknownFields, newUnknownField(field, scopeMulti, path))
		}
	}
	return multi, nil
}

func parseBitArray(data []byte, path string) (*BitArray, error) {
	fields, err := parseFields(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformedTx, path, err)
	}
	bits := &BitArray{}
	for _, field := range fields {
		switch field.Number {
		case 1:
			switch field.Type {
			case WireVarint:
				bits.Elems = append(bits.Elems, field.Varint)
			case WireBytes:
				// Proto3 numeric fields may be packed.
				off := 0
				for off < len(field.Bytes) {
					elem, err := readVarint(field.Bytes, &off)
					if err != nil {
						return nil, fmt.Errorf("%w: %s: %v", ErrMalformedTx, path, err)
					}
					bits.Elems = append(bits.Elems, elem)
				}
			default:
				return nil, fmt.Errorf(
					"%w: %s: field 1 has wire type %d, want varint or bytes",
					ErrMalformedTx,
					path,
					field.Type,
				)
			}
		default:
			bits.UnknownFields = append(bits.UnknownFields, newUnknownField(field, scopeBitArray, path))
		}
	}
	return bits, nil
}

func parseFee(data []byte, path string) (*Fee, error) {
	fields, err := parseFields(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformedTx, path, err)
	}
	fee := &Fee{}
	for _, field := range fields {
		switch field.Number {
		case 1:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			coin, err := parseCoin(field.Bytes, fmt.Sprintf("%s.amount[%d]", path, len(fee.Amount)))
			if err != nil {
				return nil, err
			}
			fee.Amount = append(fee.Amount, coin)
		case 2:
			if err := requireWire(field, WireVarint); err != nil {
				return nil, err
			}
			fee.GasLimit = field.Varint
		case 3:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			fee.Payer = string(field.Bytes)
		case 4:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			fee.Granter = string(field.Bytes)
		default:
			fee.UnknownFields = append(fee.UnknownFields, newUnknownField(field, scopeFee, path))
		}
	}
	return fee, nil
}

func parseCoin(data []byte, path string) (*Coin, error) {
	fields, err := parseFields(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformedTx, path, err)
	}
	coin := &Coin{}
	for _, field := range fields {
		switch field.Number {
		case 1:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			coin.Denom = string(field.Bytes)
		case 2:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			coin.Amount = string(field.Bytes)
		default:
			coin.UnknownFields = append(coin.UnknownFields, newUnknownField(field, scopeCoin, path))
		}
	}
	return coin, nil
}

func parseTip(data []byte, path string) (*Tip, error) {
	fields, err := parseFields(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformedTx, path, err)
	}
	tip := &Tip{}
	for _, field := range fields {
		switch field.Number {
		case 1:
			if err := requireWire(field, WireBytes); err != nil {
				return nil, err
			}
			coin, err := parseCoin(field.Bytes, fmt.Sprintf("%s.amount[%d]", path, len(tip.Amount)))
			if err != nil {
				return nil, err
			}
			tip.Amount = append(tip.Amount, coin)
		default:
			tip.UnknownFields = append(tip.UnknownFields, newUnknownField(field, scopeTip, path))
		}
	}
	return tip, nil
}

func newUnknownField(field rawField, scope, path string) UnknownField {
	return UnknownField{
		Number:   field.Number,
		Type:     field.Type,
		Scope:    scope,
		Path:     path,
		Critical: field.Number < 1024,
		Raw:      cloneBytes(field.Raw),
	}
}

func requireWire(field rawField, want WireType) error {
	if field.Type == want {
		return nil
	}
	return fmt.Errorf(
		"%w: field %d has wire type %d, want %d",
		ErrMalformedTx,
		field.Number,
		field.Type,
		want,
	)
}

func cloneBytes(data []byte) []byte {
	if data == nil {
		return nil
	}
	return append([]byte(nil), data...)
}

// IsCriticalUnknownField reports whether an unknown field number is critical.
// Field numbers below 1024 are critical; numbers at or above 1024 are
// non-critical protocol extension space.
func IsCriticalUnknownField(number int32) bool {
	return number < 1024
}

// CriticalFieldThreshold is the first non-critical protobuf field number.
const CriticalFieldThreshold int32 = 1024
