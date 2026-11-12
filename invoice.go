package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
)

// Network identifies the Bitcoin network a BOLT 11 payment request belongs to.
type Network string

const (
	Mainnet Network = "mainnet"
	Testnet Network = "testnet"
	Regtest Network = "regtest"
	Signet  Network = "signet"
)

const (
	// DefaultExpirySeconds is used when the invoice has no x field.
	DefaultExpirySeconds uint64 = 3600
	// DefaultMinFinalCLTVDelta is the BOLT 11 default when c is absent.
	DefaultMinFinalCLTVDelta uint64 = 18
	// DefaultMaxFeeRatePPM limits the compounded proportional fee of route
	// hints to 10% (100000 parts per million).
	DefaultMaxFeeRatePPM uint64 = 100_000
	// DefaultMaxCLTVExpiryDelta limits the sum of route hint CLTV deltas to
	// 2016 blocks, roughly two weeks.
	DefaultMaxCLTVExpiryDelta uint64 = 2016
)

const (
	// Tag values are bech32 symbols: 1 is "p", 16 is "s", and so on.
	tagPaymentHash       = 1  // p
	tagPaymentSecret     = 16 // s
	tagDescription       = 13 // d
	tagDescriptionHash   = 23 // h
	tagPayeePubKey       = 19 // n
	tagExpiry            = 6  // x
	tagMinFinalCLTVDelta = 24 // c
	tagRouteHint         = 3  // r
)

var (
	ErrEmptyInvoice     = errors.New("empty invoice")
	ErrInvalidInvoice   = errors.New("invalid BOLT 11 invoice")
	ErrInvalidConfig    = errors.New("invalid invoice validation config")
	ErrNetworkMismatch  = errors.New("invoice network prefix does not match configured network")
	ErrPaymentHash      = errors.New("invoice must contain exactly one p field of 52 characters")
	ErrPayeeKey         = errors.New("invoice must contain exactly one valid n payee key")
	ErrSignature        = errors.New("invoice signature does not recover to the n payee key")
	ErrInvalidExpiry    = errors.New("invalid invoice expiry field")
	ErrInvalidRouteHint = errors.New("invalid route hint field")
	ErrPolicy           = errors.New("invoice route hints exceed admission limits")
)

// Config is the caller-owned admission policy. Zero-valued limits and a zero
// default expiry select the package defaults.
type Config struct {
	Network            Network
	DefaultExpiry      uint64 // seconds; 0 means DefaultExpirySeconds
	MaxFeeRatePPM      uint64 // compounded route fee in ppm; 0 means DefaultMaxFeeRatePPM
	MaxCLTVExpiryDelta uint64 // summed route hint delta; 0 means DefaultMaxCLTVExpiryDelta
}

// NewConfig returns a policy for network with secure default limits.
func NewConfig(network Network) Config {
	return Config{Network: network}
}

// RouteHint is one r-field hop.
type RouteHint struct {
	PubKey                    []byte
	ShortChannelID            uint64
	FeeBaseMSat               uint32
	FeeProportionalMillionths uint32
	CLTVExpiryDelta           uint32
}

// Invoice is a decoded and admitted payment request.
type Invoice struct {
	Raw               string
	HRP               string
	Network           Network
	NetworkPrefix     string
	AmountMSat        uint64
	PaymentHash       [32]byte
	PaymentSecret     []byte
	Description       string
	PayeePubKey       []byte
	Timestamp         uint64
	Expiry            uint64 // effective expiry in seconds
	MinFinalCLTVDelta uint64
	RouteHints        []RouteHint

	// Effective compounded proportional fee and summed CLTV delta of the
	// route hints, in units suitable for comparison with Config limits.
	RouteHintFeePPM    uint64
	RouteHintCLTVDelta uint64
}

// ParseNetwork normalizes a caller supplied network name or BOLT 11 currency
// code. It accepts names such as "mainnet" and codes such as "bc" or "lnbc".
func ParseNetwork(value string) (Network, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.TrimPrefix(normalized, "ln")
	switch normalized {
	case "bc", "bitcoin", "main", "mainnet":
		return Mainnet, nil
	case "tb", "test", "testnet", "testnet3":
		return Testnet, nil
	case "bcrt", "reg", "regtest":
		return Regtest, nil
	case "tbs", "sb", "sig", "signet":
		return Signet, nil
	default:
		return "", fmt.Errorf("%w: unknown network %q", ErrInvalidConfig, value)
	}
}

func (n Network) prefix() string {
	switch n {
	case Mainnet:
		return "lnbc"
	case Testnet:
		return "lntb"
	case Regtest:
		return "lnbcrt"
	case Signet:
		return "lntbs"
	default:
		return ""
	}
}

// Validate checks an invoice against cfg and returns its decoded contents.
// The signature is recovered locally; no external secp256k1 implementation is
// required by the caller.
func Validate(raw string, cfg Config) (*Invoice, error) {
	return cfg.Validate(raw)
}

// ValidateInvoice is an explicit alias for Validate.
func ValidateInvoice(raw string, cfg Config) (*Invoice, error) {
	return cfg.Validate(raw)
}

// Admit is an alias for Validate.
func Admit(raw string, cfg Config) (*Invoice, error) {
	return cfg.Validate(raw)
}

// Validate applies the receiver's policy to raw.
func (cfg Config) Validate(raw string) (*Invoice, error) {
	if raw == "" {
		return nil, ErrEmptyInvoice
	}
	network, err := ParseNetwork(string(cfg.Network))
	if err != nil {
		return nil, err
	}
	if cfg.DefaultExpiry == 0 {
		cfg.DefaultExpiry = DefaultExpirySeconds
	}
	if cfg.MaxFeeRatePPM == 0 {
		cfg.MaxFeeRatePPM = DefaultMaxFeeRatePPM
	}
	if cfg.MaxCLTVExpiryDelta == 0 {
		cfg.MaxCLTVExpiryDelta = DefaultMaxCLTVExpiryDelta
	}

	inv, err := decodeInvoice(raw, cfg.DefaultExpiry, network)
	if err != nil {
		return nil, err
	}

	if err := applyRouteHintPolicy(inv, cfg.MaxFeeRatePPM, cfg.MaxCLTVExpiryDelta); err != nil {
		return nil, err
	}
	return inv, nil
}

func decodeInvoice(raw string, defaultExpiry uint64, expected Network) (*Invoice, error) {
	hrp, data, err := bech32Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInvoice, err)
	}
	if len(data) < 104+3 {
		return nil, fmt.Errorf("%w: too short for signature and tagged fields", ErrInvalidInvoice)
	}

	network, prefix, amountText, err := splitHRP(hrp)
	if err != nil {
		return nil, err
	}
	if expected != "" && network != expected {
		return nil, fmt.Errorf("%w: invoice uses %q, caller requires %q",
			ErrNetworkMismatch, prefix, expected.prefix())
	}
	amountMSat, err := parseAmountMSat(amountText)
	if err != nil {
		return nil, err
	}

	body := data[:len(data)-104]
	signatureWords := data[len(data)-104:]
	if len(body) < 7 {
		return nil, fmt.Errorf("%w: missing timestamp", ErrInvalidInvoice)
	}
	timestamp := decodeTimestamp(body[:7])
	fields := body[7:]

	inv := &Invoice{
		Raw:               raw,
		HRP:               hrp,
		Network:           network,
		NetworkPrefix:     prefix,
		AmountMSat:        amountMSat,
		Expiry:            defaultExpiry,
		PaymentSecret:     nil,
		Description:       "",
		PayeePubKey:       nil,
		Timestamp:         timestamp,
		MinFinalCLTVDelta: DefaultMinFinalCLTVDelta,
		RouteHints:        nil,
	}

	var paymentHashCount, payeeKeyCount, expiryCount int
	var encodedPayeeKey []byte

	for pos := 0; pos < len(fields); {
		if pos+3 > len(fields) {
			return nil, fmt.Errorf("%w: truncated tagged field", ErrInvalidInvoice)
		}
		tag := int(fields[pos])
		length := int(fields[pos+1])<<5 | int(fields[pos+2])
		pos += 3
		if length > len(fields)-pos {
			return nil, fmt.Errorf("%w: tagged field %d overruns invoice", ErrInvalidInvoice, tag)
		}
		payload := fields[pos : pos+length]
		pos += length

		switch tag {
		case tagPaymentHash:
			paymentHashCount++
			if len(payload) != 52 {
				return nil, fmt.Errorf("%w: p field has %d characters", ErrPaymentHash, len(payload))
			}
			hashBytes, err := convert5To8(payload, false)
			if err != nil || len(hashBytes) != 32 {
				return nil, fmt.Errorf("%w: malformed payment hash", ErrPaymentHash)
			}
			copy(inv.PaymentHash[:], hashBytes)

		case tagPaymentSecret:
			secret, err := convert5To8(payload, false)
			if err != nil || len(secret) != 32 {
				return nil, fmt.Errorf("%w: malformed payment secret", ErrInvalidInvoice)
			}
			inv.PaymentSecret = secret

		case tagDescription:
			description, err := convert5To8(payload, false)
			if err != nil {
				return nil, fmt.Errorf("%w: malformed description", ErrInvalidInvoice)
			}
			inv.Description = string(description)

		case tagPayeePubKey:
			payeeKeyCount++
			if len(payload) != 53 {
				return nil, fmt.Errorf("%w: n field must be 53 characters", ErrPayeeKey)
			}
			key, err := convert5To8(payload, false)
			if err != nil || len(key) != 33 {
				return nil, fmt.Errorf("%w: malformed n field", ErrPayeeKey)
			}
			if _, err := parsePubKey(key); err != nil {
				return nil, fmt.Errorf("%w: n field is not on secp256k1", ErrPayeeKey)
			}
			encodedPayeeKey = key

		case tagExpiry:
			expiryCount++
			expiry, err := decodeUnsigned(payload)
			if err != nil {
				return nil, fmt.Errorf("%w: malformed x field", ErrInvalidExpiry)
			}
			inv.Expiry = expiry

		case tagMinFinalCLTVDelta:
			delta, err := decodeUnsigned(payload)
			if err != nil {
				return nil, fmt.Errorf("%w: malformed c field", ErrInvalidInvoice)
			}
			inv.MinFinalCLTVDelta = delta

		case tagRouteHint:
			hints, err := parseRouteHint(payload)
			if err != nil {
				return nil, err
			}
			inv.RouteHints = append(inv.RouteHints, hints...)
		}
	}

	if paymentHashCount != 1 {
		return nil, fmt.Errorf("%w: found %d p fields", ErrPaymentHash, paymentHashCount)
	}
	if payeeKeyCount != 1 {
		return nil, fmt.Errorf("%w: found %d n fields", ErrPayeeKey, payeeKeyCount)
	}
	if expiryCount > 1 {
		return nil, fmt.Errorf("%w: found %d x fields", ErrInvalidExpiry, expiryCount)
	}

	bodyBytes, err := convert5To8(body, true)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed data section", ErrInvalidInvoice)
	}
	preimage := make([]byte, 0, len(hrp)+len(bodyBytes))
	preimage = append(preimage, hrp...)
	preimage = append(preimage, bodyBytes...)
	messageHash := sha256.Sum256(preimage)

	signature, err := convert5To8(signatureWords, false)
	if err != nil || len(signature) != 65 {
		return nil, fmt.Errorf("%w: malformed signature", ErrSignature)
	}
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:64])
	recovered, err := recoverPubKey(messageHash[:], r, s, signature[64])
	if err != nil {
		return nil, fmt.Errorf("%w: signature recovery failed", ErrSignature)
	}
	recoveredKey := compressedPubKey(recovered)

	inv.PayeePubKey = encodedPayeeKey
	if !bytesEqual(recoveredKey, encodedPayeeKey) {
		return nil, ErrSignature
	}
	return inv, nil
}

func splitHRP(hrp string) (Network, string, string, error) {
	if !strings.HasPrefix(hrp, "ln") {
		return "", "", "", fmt.Errorf("%w: missing ln prefix", ErrInvalidInvoice)
	}
	rest := hrp[2:]
	var prefix, code string
	var network Network
	switch {
	case strings.HasPrefix(rest, "bcrt"):
		prefix, code, network = "lnbcrt", "bcrt", Regtest
	case strings.HasPrefix(rest, "bc"):
		prefix, code, network = "lnbc", "bc", Mainnet
	case strings.HasPrefix(rest, "tbs"):
		prefix, code, network = "lntbs", "tbs", Signet
	case strings.HasPrefix(rest, "tb"):
		prefix, code, network = "lntb", "tb", Testnet
	case strings.HasPrefix(rest, "sb"):
		prefix, code, network = "lnsb", "sb", Signet
	default:
		return "", "", "", fmt.Errorf("%w: unsupported network prefix", ErrInvalidInvoice)
	}
	amount := rest[len(code):]
	if amount != "" {
		last := amount[len(amount)-1]
		if last < '0' || last > '9' {
			if strings.IndexByte("munp", last) < 0 {
				return "", "", "", fmt.Errorf("%w: invalid amount multiplier", ErrInvalidInvoice)
			}
			if len(amount) < 2 {
				return "", "", "", fmt.Errorf("%w: amount is missing", ErrInvalidInvoice)
			}
		}
	}
	return network, prefix, amount, nil
}

func parseAmountMSat(amount string) (uint64, error) {
	if amount == "" {
		return 0, nil
	}
	multiplier := uint64(100_000_000_000)
	number := amount
	switch amount[len(amount)-1] {
	case 'm':
		multiplier = 100_000_000
		number = amount[:len(amount)-1]
	case 'u':
		multiplier = 100_000
		number = amount[:len(amount)-1]
	case 'n':
		multiplier = 100
		number = amount[:len(amount)-1]
	case 'p':
		multiplier = 1
		number = amount[:len(amount)-1]
	}
	if number == "" {
		return 0, fmt.Errorf("%w: amount has no digits", ErrInvalidInvoice)
	}
	for i := 0; i < len(number); i++ {
		if number[i] < '0' || number[i] > '9' {
			return 0, fmt.Errorf("%w: amount is not an integer", ErrInvalidInvoice)
		}
	}
	value, ok := new(big.Int).SetString(number, 10)
	if !ok {
		return 0, fmt.Errorf("%w: invalid amount", ErrInvalidInvoice)
	}

	if amount[len(amount)-1] == 'p' {
		if new(big.Int).Mod(value, big.NewInt(10)).Sign() != 0 {
			return 0, fmt.Errorf("%w: pico amount is not a whole millisatoshi", ErrInvalidInvoice)
		}
		value.Quo(value, big.NewInt(10))
	} else {
		value.Mul(value, new(big.Int).SetUint64(multiplier))
	}
	if !value.IsUint64() {
		return 0, fmt.Errorf("%w: amount overflows millisatoshi", ErrInvalidInvoice)
	}
	return value.Uint64(), nil
}

func decodeUnsigned(data []byte) (uint64, error) {
	if len(data) == 0 || len(data) > 8 {
		return 0, errInvalidField
	}
	decoded, err := convert5To8(data, false)
	if err != nil || len(decoded) == 0 || len(decoded) > 8 {
		return 0, errInvalidField
	}
	var value uint64
	for _, b := range decoded {
		value = value<<8 | uint64(b)
	}
	return value, nil
}

func decodeTimestamp(words []byte) uint64 {
	var timestamp uint64
	for _, word := range words {
		timestamp = timestamp<<5 | uint64(word&31)
	}
	return timestamp
}

func parseRouteHint(payload []byte) ([]RouteHint, error) {
	decoded, err := convert5To8(payload, false)
	if err != nil || len(decoded) == 0 || len(decoded)%51 != 0 {
		return nil, fmt.Errorf("%w: r field is not a 51-byte hop", ErrInvalidRouteHint)
	}
	hints := make([]RouteHint, 0, len(decoded)/51)
	for len(decoded) > 0 {
		hop := decoded[:51]
		decoded = decoded[51:]
		if _, err := parsePubKey(hop[:33]); err != nil {
			return nil, fmt.Errorf("%w: hop pubkey is invalid", ErrInvalidRouteHint)
		}
		hint := RouteHint{
			PubKey:                    append([]byte(nil), hop[:33]...),
			ShortChannelID:            binary.BigEndian.Uint64(hop[33:41]),
			FeeBaseMSat:               binary.BigEndian.Uint32(hop[41:45]),
			FeeProportionalMillionths: binary.BigEndian.Uint32(hop[45:49]),
			CLTVExpiryDelta:           uint32(binary.BigEndian.Uint16(hop[49:51])),
		}
		hints = append(hints, hint)
	}
	return hints, nil
}

func applyRouteHintPolicy(inv *Invoice, maxFeePPM, maxCLTV uint64) error {
	fee := big.NewInt(1_000_000)
	base := big.NewInt(1_000_000)
	var cltv uint64
	for _, hint := range inv.RouteHints {
		numerator := new(big.Int).Add(base, big.NewInt(int64(hint.FeeProportionalMillionths)))
		fee.Mul(fee, numerator)
		fee.Quo(fee, base)

		delta := uint64(hint.CLTVExpiryDelta)
		if cltv > math.MaxUint64-delta {
			return fmt.Errorf("%w: CLTV delta overflows", ErrPolicy)
		}
		cltv += delta
	}

	feePPM := new(big.Int).Sub(fee, big.NewInt(1_000_000))
	if feePPM.Sign() < 0 {
		feePPM.SetInt64(0)
	}
	if feePPM.IsUint64() {
		inv.RouteHintFeePPM = feePPM.Uint64()
	} else {
		inv.RouteHintFeePPM = ^uint64(0)
	}
	inv.RouteHintCLTVDelta = cltv

	if !feePPM.IsUint64() || inv.RouteHintFeePPM > maxFeePPM {
		return fmt.Errorf("%w: route fee %s ppm exceeds limit %d ppm",
			ErrPolicy, feePPM.String(), maxFeePPM)
	}
	if cltv > maxCLTV {
		return fmt.Errorf("%w: route CLTV delta %d exceeds limit %d",
			ErrPolicy, cltv, maxCLTV)
	}
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
