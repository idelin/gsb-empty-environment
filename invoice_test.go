package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
)

type testTag struct {
	typ     int
	payload []byte
}

var (
	testKeyOne = testPrivateKey("netsec key one")
	testKeyTwo = testPrivateKey("netsec key two")
)

const testTimestamp uint64 = 1_700_000_000

func testPrivateKey(label string) *big.Int {
	digest := sha256.Sum256([]byte(label))
	key := new(big.Int).SetBytes(digest[:])
	key.Mod(key, secpN)
	if key.Sign() == 0 {
		panic("zero test key")
	}
	return key
}

func publicKeyFor(key *big.Int) []byte {
	return compressedPubKey(scalarBaseMult(key))
}

func signTestHash(key *big.Int, hash []byte) ([]byte, error) {
	e := new(big.Int).SetBytes(hash)
	e.Mod(e, secpN)
	for counter := 0; counter < 100; counter++ {
		nonceInput := append(append([]byte{}, key.Bytes()...), hash...)
		nonceInput = append(nonceInput, byte(counter))
		nonceHash := sha256.Sum256(nonceInput)
		k := new(big.Int).SetBytes(nonceHash[:])
		k.Mod(k, secpN)
		if k.Sign() == 0 {
			continue
		}

		R := scalarBaseMult(k)
		r := new(big.Int).Mod(R.x, secpN)
		if r.Sign() == 0 {
			continue
		}
		s := new(big.Int).Set(e)
		s.Add(s, new(big.Int).Mul(r, key))
		s.Mod(s, secpN)
		kInv := new(big.Int).ModInverse(k, secpN)
		if kInv == nil {
			continue
		}
		s.Mul(s, kInv)
		s.Mod(s, secpN)
		if s.Sign() == 0 {
			continue
		}

		recoveryID := byte(0)
		if R.y.Bit(0) == 1 {
			recoveryID = 1
		}
		if R.x.Cmp(secpN) >= 0 {
			recoveryID |= 2
		}
		if s.Cmp(new(big.Int).Rsh(secpN, 1)) > 0 {
			s.Sub(secpN, s)
			recoveryID ^= 1
		}

		out := make([]byte, 65)
		r.FillBytes(out[:32])
		s.FillBytes(out[32:64])
		out[64] = recoveryID
		return out, nil
	}
	return nil, errors.New("could not produce test signature")
}

func mustWords(t *testing.T, data []byte) []byte {
	t.Helper()
	words, err := convert8To5(data, true)
	if err != nil {
		t.Fatalf("convert to bech32 words: %v", err)
	}
	return words
}

func taggedField(t *testing.T, tag int, payload []byte) []byte {
	t.Helper()
	words := mustWords(t, payload)
	if len(words) > 1023 {
		t.Fatalf("test field too long: %d words", len(words))
	}
	out := []byte{byte(tag), byte(len(words) >> 5), byte(len(words) & 31)}
	return append(out, words...)
}

func buildTestInvoice(t *testing.T, hrp string, signer *big.Int, tags []testTag) string {
	t.Helper()
	body := timestampWords(testTimestamp)
	for _, tag := range tags {
		body = append(body, taggedField(t, tag.typ, tag.payload)...)
	}
	bodyBytes, err := convert5To8(body, true)
	if err != nil {
		t.Fatalf("convert invoice body: %v", err)
	}
	preimage := append([]byte(hrp), bodyBytes...)
	hash := sha256.Sum256(preimage)
	signature, err := signTestHash(signer, hash[:])
	if err != nil {
		t.Fatalf("sign test invoice: %v", err)
	}
	data := append(body, mustWords(t, signature)...)
	return bech32Encode(hrp, data)
}

func timestampWords(timestamp uint64) []byte {
	words := make([]byte, 7)
	for i := 6; i >= 0; i-- {
		words[i] = byte(timestamp & 31)
		timestamp >>= 5
	}
	return words
}

func baseTags(payeeKey []byte, extra ...testTag) []testTag {
	paymentHash := sha256.Sum256([]byte("payment preimage"))
	tags := []testTag{
		{typ: tagPaymentHash, payload: paymentHash[:]},
		{typ: tagDescription, payload: []byte("test invoice")},
		{typ: tagPayeePubKey, payload: payeeKey},
	}
	return append(tags, extra...)
}

func routeHintPayload(t *testing.T, pubKey []byte, base, proportional, cltv uint32) []byte {
	t.Helper()
	if len(pubKey) != 33 {
		t.Fatalf("route hint pubkey must be 33 bytes")
	}
	out := make([]byte, 51)
	copy(out[:33], pubKey)
	// Short channel ID 1:1:1.
	out[33] = 0x00
	out[34] = 0x00
	out[35] = 0x01
	out[36] = 0x00
	out[37] = 0x00
	out[38] = 0x01
	out[39] = 0x00
	out[40] = 0x01
	putUint32(out[41:45], base)
	putUint32(out[45:49], proportional)
	out[49] = byte(cltv >> 8)
	out[50] = byte(cltv)
	return out
}

func putUint32(dst []byte, value uint32) {
	dst[0] = byte(value >> 24)
	dst[1] = byte(value >> 16)
	dst[2] = byte(value >> 8)
	dst[3] = byte(value)
}

func TestValidateValidInvoiceAndDefaultExpiry(t *testing.T) {
	payee := publicKeyFor(testKeyOne)
	hops := []testTag{
		{typ: tagRouteHint, payload: routeHintPayload(t, publicKeyFor(testKeyTwo), 1000, 2000, 40)},
		{typ: tagRouteHint, payload: routeHintPayload(t, publicKeyFor(testKeyTwo), 500, 1000, 40)},
	}
	invoice := buildTestInvoice(t, "lnbc10u", testKeyOne, baseTags(payee, hops...))

	inv, err := Validate(invoice, Config{Network: Mainnet})
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if inv.Network != Mainnet || inv.NetworkPrefix != "lnbc" {
		t.Fatalf("unexpected network: %s %s", inv.Network, inv.NetworkPrefix)
	}
	if inv.AmountMSat != 1_000_000 {
		t.Fatalf("amount = %d msat, want 1000000", inv.AmountMSat)
	}
	if inv.Expiry != DefaultExpirySeconds {
		t.Fatalf("expiry = %d, want %d", inv.Expiry, DefaultExpirySeconds)
	}
	if inv.Timestamp != testTimestamp {
		t.Fatalf("timestamp = %d, want %d", inv.Timestamp, testTimestamp)
	}
	if !bytes.Equal(inv.PayeePubKey, payee) {
		t.Fatalf("payee key = %x, want %x", inv.PayeePubKey, payee)
	}
	if len(inv.RouteHints) != 2 {
		t.Fatalf("route hints = %d, want 2", len(inv.RouteHints))
	}
	if inv.RouteHints[0].FeeBaseMSat != 1000 ||
		inv.RouteHints[0].FeeProportionalMillionths != 2000 ||
		inv.RouteHints[0].CLTVExpiryDelta != 40 {
		t.Fatalf("unexpected first route hint: %+v", inv.RouteHints[0])
	}
	if inv.RouteHintCLTVDelta != 80 {
		t.Fatalf("route CLTV = %d, want 80", inv.RouteHintCLTVDelta)
	}
	if inv.RouteHintFeePPM == 0 || inv.RouteHintFeePPM > 4000 {
		t.Fatalf("compounded route fee = %d ppm, want small positive fee", inv.RouteHintFeePPM)
	}
}

func TestExplicitExpiry(t *testing.T) {
	payee := publicKeyFor(testKeyOne)
	tags := baseTags(payee, testTag{typ: tagExpiry, payload: []byte{0x01, 0x2c}})
	invoice := buildTestInvoice(t, "lnbc1", testKeyOne, tags)

	inv, err := Validate(invoice, Config{Network: Mainnet})
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if inv.Expiry != 300 {
		t.Fatalf("expiry = %d, want 300", inv.Expiry)
	}
}

func TestCrossNetworkInvoiceRejected(t *testing.T) {
	payee := publicKeyFor(testKeyOne)
	mainnetInvoice := buildTestInvoice(t, "lnbc10u", testKeyOne, baseTags(payee))

	for _, test := range []struct {
		name    string
		cfg     Config
		invoice string
	}{
		{"mainnet invoice on testnet node", Config{Network: Testnet}, mainnetInvoice},
		{"testnet invoice on mainnet node", Config{Network: Mainnet}, buildTestInvoice(t, "lntb10u", testKeyOne, baseTags(payee))},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Validate(test.invoice, test.cfg); !errors.Is(err, ErrNetworkMismatch) {
				t.Fatalf("error = %v, want ErrNetworkMismatch", err)
			}
		})
	}
}

func TestTamperedPayeeKeyRejected(t *testing.T) {
	// The signature is made by testKeyOne, but the body carries testKeyTwo in
	// n. A valid bech32 checksum alone must not make this invoice admissible.
	invoice := buildTestInvoice(t, "lnbc10u", testKeyOne, baseTags(publicKeyFor(testKeyTwo)))

	_, err := Validate(invoice, Config{Network: Mainnet})
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("error = %v, want ErrSignature", err)
	}
}

func TestPaymentHashFieldIsExactlyOneAnd52Chars(t *testing.T) {
	payee := publicKeyFor(testKeyOne)
	shortDigest := sha256.Sum256([]byte("short"))
	shortHash := shortDigest[:20]
	tags := []testTag{
		{typ: tagPaymentHash, payload: shortHash},
		{typ: tagDescription, payload: []byte("bad p")},
		{typ: tagPayeePubKey, payload: payee},
	}
	invoice := buildTestInvoice(t, "lnbc10u", testKeyOne, tags)
	if _, err := Validate(invoice, Config{Network: Mainnet}); !errors.Is(err, ErrPaymentHash) {
		t.Fatalf("short p error = %v, want ErrPaymentHash", err)
	}

	hashTwo := sha256.Sum256([]byte("two"))
	tags = baseTags(payee, testTag{typ: tagPaymentHash, payload: hashTwo[:]})
	invoice = buildTestInvoice(t, "lnbc10u", testKeyOne, tags)
	if _, err := Validate(invoice, Config{Network: Mainnet}); !errors.Is(err, ErrPaymentHash) {
		t.Fatalf("duplicate p error = %v, want ErrPaymentHash", err)
	}

	tags = []testTag{
		{typ: tagDescription, payload: []byte("missing p")},
		{typ: tagPayeePubKey, payload: payee},
	}
	invoice = buildTestInvoice(t, "lnbc10u", testKeyOne, tags)
	if _, err := Validate(invoice, Config{Network: Mainnet}); !errors.Is(err, ErrPaymentHash) {
		t.Fatalf("missing p error = %v, want ErrPaymentHash", err)
	}
}

func TestRouteHintFeeAndCLTVLimits(t *testing.T) {
	payee := publicKeyFor(testKeyOne)
	hopKey := publicKeyFor(testKeyTwo)

	t.Run("compounded fee", func(t *testing.T) {
		hops := []testTag{
			{typ: tagRouteHint, payload: routeHintPayload(t, hopKey, 0, 200_000, 40)},
			{typ: tagRouteHint, payload: routeHintPayload(t, hopKey, 0, 200_000, 40)},
		}
		invoice := buildTestInvoice(t, "lnbc10u", testKeyOne, baseTags(payee, hops...))
		_, err := Validate(invoice, Config{Network: Mainnet})
		if !errors.Is(err, ErrPolicy) {
			t.Fatalf("high fee error = %v, want ErrPolicy", err)
		}
	})

	t.Run("cltv delta", func(t *testing.T) {
		hops := []testTag{
			{typ: tagRouteHint, payload: routeHintPayload(t, hopKey, 0, 100, 2000)},
			{typ: tagRouteHint, payload: routeHintPayload(t, hopKey, 0, 100, 2000)},
		}
		invoice := buildTestInvoice(t, "lnbc10u", testKeyOne, baseTags(payee, hops...))
		_, err := Validate(invoice, Config{Network: Mainnet})
		if !errors.Is(err, ErrPolicy) {
			t.Fatalf("high CLTV error = %v, want ErrPolicy", err)
		}
	})

	t.Run("custom limits", func(t *testing.T) {
		hops := []testTag{
			{typ: tagRouteHint, payload: routeHintPayload(t, hopKey, 0, 200_000, 40)},
			{typ: tagRouteHint, payload: routeHintPayload(t, hopKey, 0, 200_000, 40)},
		}
		invoice := buildTestInvoice(t, "lnbc10u", testKeyOne, baseTags(payee, hops...))
		inv, err := Validate(invoice, Config{
			Network:            Mainnet,
			MaxFeeRatePPM:      500_000,
			MaxCLTVExpiryDelta: 200,
		})
		if err != nil {
			t.Fatalf("custom limits rejected invoice: %v", err)
		}
		if inv.RouteHintFeePPM != 440_000 {
			t.Fatalf("compounded fee = %d, want 440000", inv.RouteHintFeePPM)
		}
	})
}

func TestKnownBOLT11SignatureVector(t *testing.T) {
	// BOLT 11 specification example ("Please send 0.0025 BTC...").
	const invoice = "lnbc2500u1pvjluezpp5qqqsyqcyq5rqwzqfqqqsyqcyq5rqwzqfqqqsyqcyq5rqwzqfqypqdq5xysxxatsyp3k7enxv4jsxqzpuaztrnwngzn3kdzw5hydlzf03qdgm2hdq27cqv3agm2awhz5se903vruatfhq77w3ls4evs3ch9zw97j25emudupq63nyw24cg27h2rspfj9srp"
	expected := "03e7156ae33b0a208d0744199163177e909e80176e55d97a2f221ede0f934dd9ad"

	hrp, data, err := bech32Decode(invoice)
	if err != nil {
		t.Fatalf("decode known invoice: %v", err)
	}
	signature, err := convert5To8(data[len(data)-104:], false)
	if err != nil {
		t.Fatalf("decode known signature: %v", err)
	}
	bodyBytes, err := convert5To8(data[:len(data)-104], true)
	if err != nil {
		t.Fatalf("decode known body: %v", err)
	}
	hash := sha256.Sum256(append([]byte(hrp), bodyBytes...))
	point, err := recoverPubKey(hash[:],
		new(big.Int).SetBytes(signature[:32]),
		new(big.Int).SetBytes(signature[32:64]),
		signature[64])
	if err != nil {
		t.Fatalf("recover known signature: %v", err)
	}
	got := hex.EncodeToString(compressedPubKey(point))
	if got != expected {
		t.Fatalf("recovered key = %s, want %s", got, expected)
	}

	// The specification example predates the optional n field, so admission
	// must fail with a payee-key error after successfully parsing p, d, and x.
	if _, err := decodeInvoice(invoice, DefaultExpirySeconds, Mainnet); !errors.Is(err, ErrPayeeKey) {
		t.Fatalf("decode known invoice error = %v, want ErrPayeeKey", err)
	}
}
