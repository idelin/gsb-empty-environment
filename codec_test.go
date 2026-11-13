package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func authForMode(mode SignMode) *AuthInfo {
	return &AuthInfo{
		SignerInfos: []*SignerInfo{
			{
				ModeInfo: &ModeInfo{Single: &Single{Mode: mode}},
			},
		},
	}
}

func marshalBodyAndAuth(t *testing.T, body *TxBody, auth *AuthInfo) []byte {
	t.Helper()
	bodyBytes, err := MarshalTxBody(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	authBytes, err := MarshalAuthInfo(auth)
	if err != nil {
		t.Fatalf("marshal auth info: %v", err)
	}
	out := appendBytesField(nil, 1, bodyBytes)
	out = appendBytesField(out, 2, authBytes)
	return out
}

func marshalTxWithBodyField(t *testing.T, body *TxBody, auth *AuthInfo, field rawField) []byte {
	t.Helper()
	bodyBytes, err := MarshalTxBody(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	bodyBytes = append(bodyBytes, field.Raw...)
	authBytes, err := MarshalAuthInfo(auth)
	if err != nil {
		t.Fatalf("marshal auth info: %v", err)
	}
	out := appendBytesField(nil, 1, bodyBytes)
	out = appendBytesField(out, 2, authBytes)
	return out
}

func bytesUnknownField(number int32, value string) rawField {
	raw := appendBytesField(nil, number, []byte(value))
	return rawField{
		Number: number,
		Type:   WireBytes,
		Bytes:  []byte(value),
		Raw:    raw,
	}
}

func varintUnknownField(number int32, value uint64) rawField {
	raw := appendVarintField(nil, number, value)
	return rawField{
		Number: number,
		Type:   WireVarint,
		Varint: value,
		Raw:    raw,
	}
}

func registeredRegistry() MapAminoRegistry {
	return MapAminoRegistry{
		"/test.MsgSend": "test/MsgSend",
	}
}

func TestExtensionOptionsAlwaysRejected(t *testing.T) {
	t.Parallel()

	for _, mode := range []SignMode{SignModeDirect, SignModeLegacyAminoJSON} {
		mode := mode
		t.Run("mode_"+modeName(mode), func(t *testing.T) {
			body := &TxBody{
				Messages: []*Any{{TypeURL: "/test.MsgSend", Value: []byte{1, 2, 3}}},
				ExtensionOptions: []*Any{
					{TypeURL: "/test.Extension", Value: []byte{1}},
				},
			}
			tx := marshalBodyAndAuth(t, body, authForMode(mode))
			_, err := DecodeTx(tx, WithAminoRegistry(registeredRegistry()))
			if !errors.Is(err, ErrExtensionOptions) {
				t.Fatalf("DecodeTx() error = %v, want %v", err, ErrExtensionOptions)
			}
		})
	}
}

func TestEmptyExtensionOptionsAccepted(t *testing.T) {
	t.Parallel()

	body := &TxBody{Messages: []*Any{{TypeURL: "/test.MsgSend", Value: []byte{1}}}}
	tx := marshalBodyAndAuth(t, body, authForMode(SignModeDirect))
	if _, err := DecodeTx(tx); err != nil {
		t.Fatalf("DecodeTx() error = %v, want nil", err)
	}
}

func TestHiddenNonCriticalUnknownFieldIgnoredOnlyForDirect(t *testing.T) {
	t.Parallel()

	body := &TxBody{Messages: []*Any{{TypeURL: "/test.MsgSend", Value: []byte{1}}}}

	directRaw := marshalTxWithBodyField(
		t,
		body,
		authForMode(SignModeDirect),
		bytesUnknownField(2048, "hidden-non-critical"),
	)
	decoded, err := DecodeTx(directRaw, WithAminoRegistry(registeredRegistry()))
	if err != nil {
		t.Fatalf("direct DecodeTx() error = %v, want nil", err)
	}
	fields := decoded.AllUnknownFields()
	if len(fields) != 1 {
		t.Fatalf("unknown fields = %d, want 1", len(fields))
	}
	if fields[0].Number != 2048 || fields[0].Critical {
		t.Fatalf("unknown field = %+v, want non-critical field 2048", fields[0])
	}
	if fields[0].Scope != scopeTxBody || fields[0].Path != "tx.body" {
		t.Fatalf("unknown field scope/path = %q/%q", fields[0].Scope, fields[0].Path)
	}

	legacyRaw := marshalTxWithBodyField(
		t,
		body,
		authForMode(SignModeLegacyAminoJSON),
		bytesUnknownField(2048, "hidden-non-critical"),
	)
	_, err = DecodeTx(legacyRaw, WithAminoRegistry(registeredRegistry()))
	if !errors.Is(err, ErrNonCriticalUnknownField) {
		t.Fatalf("legacy DecodeTx() error = %v, want %v", err, ErrNonCriticalUnknownField)
	}
}

func TestUnknownFieldNumberBoundary(t *testing.T) {
	t.Parallel()

	body := &TxBody{Messages: []*Any{{TypeURL: "/test.MsgSend"}}}

	nonCritical := marshalTxWithBodyField(
		t,
		body,
		authForMode(SignModeDirect),
		varintUnknownField(1024, 7),
	)
	if _, err := DecodeTx(nonCritical); err != nil {
		t.Fatalf("field 1024 under direct: error = %v, want nil", err)
	}

	critical := marshalTxWithBodyField(
		t,
		body,
		authForMode(SignModeDirect),
		varintUnknownField(1000, 7),
	)
	_, err := DecodeTx(critical)
	if !errors.Is(err, ErrCriticalUnknownField) {
		t.Fatalf("field 1000 under direct: error = %v, want %v", err, ErrCriticalUnknownField)
	}

	_, err = DecodeTx(critical, WithSignMode(SignModeLegacyAminoJSON))
	if !errors.Is(err, ErrCriticalUnknownField) {
		t.Fatalf("field 1000 under legacy: error = %v, want %v", err, ErrCriticalUnknownField)
	}
}

func TestCriticalUnknownFieldInAuthInfoAlwaysRejected(t *testing.T) {
	t.Parallel()

	body := &TxBody{Messages: []*Any{{TypeURL: "/test.MsgSend"}}}
	authBytes, err := MarshalAuthInfo(authForMode(SignModeDirect))
	if err != nil {
		t.Fatalf("marshal auth info: %v", err)
	}
	authBytes = append(authBytes, bytesUnknownField(999, "critical").Raw...)

	bodyBytes, err := MarshalTxBody(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	raw := appendBytesField(nil, 1, bodyBytes)
	raw = appendBytesField(raw, 2, authBytes)

	_, err = DecodeTx(raw)
	if !errors.Is(err, ErrCriticalUnknownField) {
		t.Fatalf("DecodeTx() error = %v, want %v", err, ErrCriticalUnknownField)
	}
}

func TestUnknownFieldsInAnyEnvelopeFollowSignMode(t *testing.T) {
	t.Parallel()

	anyBytes := appendStringField(nil, 1, "/test.MsgSend")
	anyBytes = append(anyBytes, bytesUnknownField(2048, "hidden-in-any").Raw...)
	bodyBytes := appendBytesField(nil, 1, anyBytes)
	authBytes, err := MarshalAuthInfo(authForMode(SignModeDirect))
	if err != nil {
		t.Fatalf("marshal auth info: %v", err)
	}
	raw := appendBytesField(nil, 1, bodyBytes)
	raw = appendBytesField(raw, 2, authBytes)

	decoded, err := DecodeTx(raw, WithAminoRegistry(registeredRegistry()))
	if err != nil {
		t.Fatalf("direct DecodeTx() error = %v, want nil", err)
	}
	if got := decoded.AllUnknownFields(); len(got) != 1 || got[0].Scope != scopeAny {
		t.Fatalf("unknown fields = %+v, want one Any field", got)
	}

	_, err = DecodeTx(raw, WithSignMode(SignModeLegacyAminoJSON))
	if !errors.Is(err, ErrNonCriticalUnknownField) {
		t.Fatalf("legacy DecodeTx() error = %v, want %v", err, ErrNonCriticalUnknownField)
	}

	criticalAny := appendStringField(nil, 1, "/test.MsgSend")
	criticalAny = append(criticalAny, bytesUnknownField(500, "critical-in-any").Raw...)
	criticalBody := appendBytesField(nil, 1, criticalAny)
	criticalRaw := appendBytesField(nil, 1, criticalBody)
	criticalRaw = appendBytesField(criticalRaw, 2, authBytes)

	_, err = DecodeTx(criticalRaw)
	if !errors.Is(err, ErrCriticalUnknownField) {
		t.Fatalf("critical Any field: error = %v, want %v", err, ErrCriticalUnknownField)
	}
}

func TestUnregisteredAminoMessage(t *testing.T) {
	t.Parallel()

	body := &TxBody{Messages: []*Any{{TypeURL: "/test.Unregistered", Value: []byte{1}}}}
	legacyRaw := marshalBodyAndAuth(t, body, authForMode(SignModeLegacyAminoJSON))

	_, err := DecodeTx(legacyRaw)
	if !errors.Is(err, ErrAminoMessageNotRegistered) {
		t.Fatalf("nil registry: error = %v, want %v", err, ErrAminoMessageNotRegistered)
	}

	_, err = DecodeTx(legacyRaw, WithAminoRegistry(registeredRegistry()))
	if !errors.Is(err, ErrAminoMessageNotRegistered) {
		t.Fatalf("wrong registry: error = %v, want %v", err, ErrAminoMessageNotRegistered)
	}

	_, err = DecodeTx(legacyRaw, WithAminoRegistry(map[string]string{
		"/test.Unregistered": "",
	}))
	if !errors.Is(err, ErrAminoMessageNotRegistered) {
		t.Fatalf("empty amino name: error = %v, want %v", err, ErrAminoMessageNotRegistered)
	}

	if _, err := DecodeTx(legacyRaw, WithAminoRegistry(map[string]string{
		"test.Unregistered": "test/Unregistered",
	})); err != nil {
		t.Fatalf("registered without leading slash: error = %v, want nil", err)
	}

	directRaw := marshalBodyAndAuth(t, body, authForMode(SignModeDirect))
	if _, err := DecodeTx(directRaw); err != nil {
		t.Fatalf("direct unregistered message: error = %v, want nil", err)
	}
}

func TestLegacyAminoDeprecationHeightAndWhitelist(t *testing.T) {
	t.Parallel()

	const (
		deprecationHeight = uint64(100)
		allowedAddress    = "cosmos1allowed"
		otherAddress      = "cosmos1other"
	)

	policy := Policy{
		AminoRegistry:                    registeredRegistry(),
		LegacyAminoJSONDeprecationHeight: deprecationHeight,
		LegacyAminoJSONWhitelist:         []string{allowedAddress},
	}
	body := &TxBody{Messages: []*Any{{TypeURL: "/test.MsgSend", Value: []byte{1}}}}
	legacyRaw := marshalBodyAndAuth(t, body, authForMode(SignModeLegacyAminoJSON))

	cases := []struct {
		name    string
		height  uint64
		signers []string
		wantErr error
	}{
		{
			name:    "one block before deprecation allows any signer",
			height:  deprecationHeight - 1,
			signers: []string{otherAddress},
		},
		{
			name:    "deprecation block rejects non-whitelisted signer",
			height:  deprecationHeight,
			signers: []string{otherAddress},
			wantErr: ErrLegacyAminoJSONDeprecated,
		},
		{
			name:    "after deprecation rejects non-whitelisted signer",
			height:  deprecationHeight + 1,
			signers: []string{otherAddress},
			wantErr: ErrLegacyAminoJSONDeprecated,
		},
		{
			name:    "deprecation block allows whitelisted signer",
			height:  deprecationHeight,
			signers: []string{allowedAddress},
		},
		{
			name:    "after deprecation allows whitelisted signer",
			height:  deprecationHeight + 1,
			signers: []string{allowedAddress},
		},
		{
			name:    "mixed signer set must be fully whitelisted",
			height:  deprecationHeight,
			signers: []string{allowedAddress, otherAddress},
			wantErr: ErrLegacyAminoJSONDeprecated,
		},
		{
			name:    "missing signers fail closed",
			height:  deprecationHeight,
			signers: nil,
			wantErr: ErrLegacyAminoJSONDeprecated,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			request := Request{Height: tc.height, Signers: tc.signers}
			_, err := policy.ValidateRawTx(legacyRaw, request)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateRawTx() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateRawTx() error = %v, want %v", err, tc.wantErr)
			}
		})
	}

	directRaw := marshalBodyAndAuth(t, body, authForMode(SignModeDirect))
	if _, err := policy.ValidateRawTx(directRaw, Request{
		Height:  deprecationHeight,
		Signers: []string{otherAddress},
	}); err != nil {
		t.Fatalf("direct at deprecation height: error = %v, want nil", err)
	}
}

func TestLegacyAminoDeprecationMissingSignerDetail(t *testing.T) {
	t.Parallel()

	policy := Policy{
		AminoRegistry:                    registeredRegistry(),
		LegacyAminoJSONDeprecationHeight: 10,
		LegacyAminoJSONWhitelist:         []string{"cosmos1allowed"},
	}
	body := &TxBody{Messages: []*Any{{TypeURL: "/test.MsgSend"}}}
	legacyRaw := marshalBodyAndAuth(t, body, authForMode(SignModeLegacyAminoJSON))

	_, err := policy.ValidateRawTx(legacyRaw, Request{Height: 10})
	if !errors.Is(err, ErrLegacyAminoJSONDeprecated) {
		t.Fatalf("error = %v, want wrapped %v", err, ErrLegacyAminoJSONDeprecated)
	}
	if !errors.Is(err, ErrNoLegacyAminoSigner) {
		t.Fatalf("error = %v, want wrapped %v", err, ErrNoLegacyAminoSigner)
	}
}

func TestZeroDeprecationHeightDisablesGate(t *testing.T) {
	t.Parallel()

	policy := Policy{AminoRegistry: registeredRegistry()}
	body := &TxBody{Messages: []*Any{{TypeURL: "/test.MsgSend"}}}
	legacyRaw := marshalBodyAndAuth(t, body, authForMode(SignModeLegacyAminoJSON))

	if _, err := policy.ValidateRawTx(legacyRaw, Request{
		Height:  1_000_000,
		Signers: []string{"cosmos1other"},
	}); err != nil {
		t.Fatalf("ValidateRawTx() error = %v, want nil", err)
	}
}

func TestCarriedTxContextIsUsedWhenRequestIsEmpty(t *testing.T) {
	t.Parallel()

	policy := Policy{
		AminoRegistry:                    registeredRegistry(),
		LegacyAminoJSONDeprecationHeight: 50,
		LegacyAminoJSONWhitelist:         []string{"cosmos1allowed"},
	}
	body := &TxBody{Messages: []*Any{{TypeURL: "/test.MsgSend"}}}
	legacyRaw := marshalBodyAndAuth(t, body, authForMode(SignModeLegacyAminoJSON))
	decoded, err := ParseTx(legacyRaw)
	if err != nil {
		t.Fatalf("ParseTx() error = %v", err)
	}
	decoded.Height = 50
	decoded.Signers = []string{"cosmos1other"}

	err = policy.Validate(decoded, Request{})
	if !errors.Is(err, ErrLegacyAminoJSONDeprecated) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrLegacyAminoJSONDeprecated)
	}

	decoded.Signers = []string{"cosmos1allowed"}
	if err := policy.Validate(decoded, Request{}); err != nil {
		t.Fatalf("Validate() with allowlisted carried signer: error = %v, want nil", err)
	}
}

func TestNonCriticalExtensionOptions(t *testing.T) {
	t.Parallel()

	body := &TxBody{
		Messages: []*Any{{TypeURL: "/test.MsgSend"}},
		NonCriticalExtensionOptions: []*Any{
			{TypeURL: "/test.NonCritical", Value: []byte{1}},
		},
	}
	directRaw := marshalBodyAndAuth(t, body, authForMode(SignModeDirect))
	if _, err := DecodeTx(directRaw); err != nil {
		t.Fatalf("direct non-critical extension: error = %v, want nil", err)
	}

	legacyRaw := marshalBodyAndAuth(t, body, authForMode(SignModeLegacyAminoJSON))
	_, err := DecodeTx(legacyRaw, WithAminoRegistry(registeredRegistry()))
	if !errors.Is(err, ErrNonCriticalExtensionOptions) {
		t.Fatalf("legacy non-critical extension: error = %v, want %v", err, ErrNonCriticalExtensionOptions)
	}
}

func TestMarshalParseRoundTrip(t *testing.T) {
	t.Parallel()

	body := &TxBody{
		Messages: []*Any{
			{TypeURL: "/test.MsgSend", Value: []byte{1, 2, 3}},
			{TypeURL: "/test.MsgOther", Value: []byte{4, 5}},
		},
		Memo:          "hello",
		TimeoutHeight: 42,
	}
	auth := &AuthInfo{
		SignerInfos: []*SignerInfo{
			{
				PublicKey: &Any{TypeURL: "/cosmos.crypto.ed25519.PubKey", Value: []byte{9}},
				ModeInfo:  &ModeInfo{Single: &Single{Mode: SignModeDirect}},
				Sequence:  7,
			},
		},
		Fee: &Fee{
			Amount:   []*Coin{{Denom: "stake", Amount: "10"}},
			GasLimit: 100,
			Payer:    "cosmos1payer",
			Granter:  "cosmos1granter",
		},
	}
	raw, err := MarshalTx(&Tx{
		Body:       body,
		AuthInfo:   auth,
		Signatures: [][]byte{{1}, {2, 3}},
	})
	if err != nil {
		t.Fatalf("MarshalTx() error = %v", err)
	}

	decoded, err := ParseTx(raw)
	if err != nil {
		t.Fatalf("ParseTx() error = %v", err)
	}
	if len(decoded.Body.Messages) != 2 || decoded.Body.Memo != "hello" || decoded.Body.TimeoutHeight != 42 {
		t.Fatalf("decoded body = %+v", decoded.Body)
	}
	if len(decoded.Signatures) != 2 || !bytes.Equal(decoded.Signatures[1], []byte{2, 3}) {
		t.Fatalf("decoded signatures = %v", decoded.Signatures)
	}
	mode := decoded.SignModes()
	if len(mode) != 1 || mode[0] != SignModeDirect {
		t.Fatalf("decoded modes = %v", mode)
	}
	if decoded.AuthInfo.Fee == nil || decoded.AuthInfo.Fee.GasLimit != 100 {
		t.Fatalf("decoded fee = %+v", decoded.AuthInfo.Fee)
	}
	if len(decoded.AllUnknownFields()) != 0 {
		t.Fatalf("unexpected unknown fields: %+v", decoded.AllUnknownFields())
	}
}

func TestMalformedTransactionsRejected(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  []byte
	}{
		{name: "zero field number", raw: []byte{0}},
		{name: "truncated varint", raw: []byte{0x08}},
		{name: "truncated bytes", raw: []byte{0x0a, 0x05, 0x01}},
		{name: "unexpected end group", raw: []byte{0x0c}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseTx(tc.raw)
			if !errors.Is(err, ErrMalformedTx) {
				t.Fatalf("ParseTx() error = %v, want %v", err, ErrMalformedTx)
			}
		})
	}

	body := &TxBody{Messages: []*Any{{TypeURL: "/test.MsgSend"}}}
	bodyBytes, err := MarshalTxBody(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	wrongWireType := appendBytesField(nil, 1, bodyBytes)
	wrongWireType = appendVarintField(wrongWireType, 2, 1)
	if _, err := ParseTx(wrongWireType); !errors.Is(err, ErrMalformedTx) {
		t.Fatalf("wrong wire type: error = %v, want %v", err, ErrMalformedTx)
	}
}

func TestEmptyAnyInsideExtensionOptionsStillRejected(t *testing.T) {
	t.Parallel()

	body := &TxBody{
		Messages:         []*Any{{TypeURL: "/test.MsgSend"}},
		ExtensionOptions: []*Any{{}},
	}
	raw := marshalBodyAndAuth(t, body, authForMode(SignModeDirect))
	_, err := DecodeTx(raw)
	if !errors.Is(err, ErrExtensionOptions) {
		t.Fatalf("DecodeTx() error = %v, want %v", err, ErrExtensionOptions)
	}
}

func TestAminoRegistryLookupForms(t *testing.T) {
	t.Parallel()

	type urlRegistry map[string]string
	type setRegistry map[string]struct{}
	cases := []struct {
		name     string
		registry any
		wantName string
		wantOK   bool
	}{
		{
			name:     "named string map",
			registry: urlRegistry{"/test.MsgSend": "test/MsgSend"},
			wantName: "test/MsgSend",
			wantOK:   true,
		},
		{
			name:     "bool map",
			registry: map[string]bool{"/test.MsgSend": true},
			wantName: "/test.MsgSend",
			wantOK:   true,
		},
		{
			name:     "string slice",
			registry: []string{"test.MsgSend"},
			wantName: "test.MsgSend",
			wantOK:   true,
		},
		{
			name:     "named set map",
			registry: setRegistry{"/test.MsgSend": {}},
			wantName: "/test.MsgSend",
			wantOK:   true,
		},
		{
			name:     "reverse amino name map",
			registry: map[string]string{"test/MsgSend": "/test.MsgSend"},
			wantName: "test/MsgSend",
			wantOK:   true,
		},
		{
			name:     "function",
			registry: func(typeURL string) (string, bool) { return "test/MsgSend", typeURL == "/test.MsgSend" },
			wantName: "test/MsgSend",
			wantOK:   true,
		},
		{
			name:     "missing",
			registry: map[string]string{"/other.Msg": "other/Msg"},
			wantOK:   false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			name, ok := LookupAminoName(tc.registry, "/test.MsgSend")
			if ok != tc.wantOK || name != tc.wantName {
				t.Fatalf("LookupAminoName() = %q, %v, want %q, %v", name, ok, tc.wantName, tc.wantOK)
			}
		})
	}
}

func FuzzParseTx(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x0a, 0x00})
	f.Add([]byte{0x0a, 0x03, 0x0a, 0x01, 0x2f})
	f.Add([]byte{0x0c})
	f.Add([]byte{0x1b, 0x08, 0x01, 0x1c})

	f.Fuzz(func(t *testing.T, raw []byte) {
		tx, err := ParseTx(raw)
		if err != nil {
			return
		}
		if tx == nil {
			t.Fatal("ParseTx returned nil transaction without an error")
		}
		_ = tx.AllUnknownFields()
		_ = tx.SignModes()
	})
}

func modeName(mode SignMode) string {
	switch mode {
	case SignModeDirect:
		return "direct"
	case SignModeLegacyAminoJSON:
		return "legacy_amino"
	default:
		return "unknown"
	}
}
