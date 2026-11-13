package netsec

// SignMode is the Cosmos transaction signing mode enum.
type SignMode int32

const (
	SignModeUnspecified     SignMode = 0
	SignModeDirect          SignMode = 1
	SignModeTextual         SignMode = 2
	SignModeDirectAux       SignMode = 3
	SignModeLegacyAminoJSON SignMode = 127
)

// Proto-style aliases mirror cosmos.tx.signing.v1beta1.SignMode names.
const (
	SignMode_SIGN_MODE_UNSPECIFIED       = SignModeUnspecified
	SignMode_SIGN_MODE_DIRECT            = SignModeDirect
	SignMode_SIGN_MODE_TEXTUAL           = SignModeTextual
	SignMode_SIGN_MODE_DIRECT_AUX        = SignModeDirectAux
	SignMode_SIGN_MODE_LEGACY_AMINO_JSON = SignModeLegacyAminoJSON
)

// Short aliases are convenient in application code.
const (
	SIGN_MODE_UNSPECIFIED       = SignModeUnspecified
	SIGN_MODE_DIRECT            = SignModeDirect
	SIGN_MODE_TEXTUAL           = SignModeTextual
	SIGN_MODE_DIRECT_AUX        = SignModeDirectAux
	SIGN_MODE_LEGACY_AMINO_JSON = SignModeLegacyAminoJSON
)

// Any is the protobuf google.protobuf.Any envelope used by Cosmos transactions.
type Any struct {
	TypeURL       string
	Value         []byte
	UnknownFields []UnknownField
}

// UnknownField describes a protobuf field that is not part of the transaction
// schema understood by this package. Critical fields use numbers below 1024.
type UnknownField struct {
	Number   int32
	Type     WireType
	Scope    string
	Path     string
	Critical bool
	Raw      []byte
}

// Tx is the decoded protobuf Cosmos transaction.
type Tx struct {
	Body          *TxBody
	AuthInfo      *AuthInfo
	Signatures    [][]byte
	UnknownFields []UnknownField

	// Height and Signers are optional admission context carried with a decoded
	// transaction. Explicit values supplied to a request take precedence.
	Height  uint64
	Signers []string

	Raw []byte
}

// TxBody is the protobuf TxBody message.
type TxBody struct {
	Messages                    []*Any
	Memo                        string
	TimeoutHeight               uint64
	ExtensionOptions            []*Any
	NonCriticalExtensionOptions []*Any
	UnknownFields               []UnknownField
}

// AuthInfo is the protobuf AuthInfo message.
type AuthInfo struct {
	SignerInfos   []*SignerInfo
	Fee           *Fee
	Tip           *Tip
	UnknownFields []UnknownField
}

// SignerInfo is the protobuf SignerInfo message.
type SignerInfo struct {
	PublicKey     *Any
	ModeInfo      *ModeInfo
	Sequence      uint64
	UnknownFields []UnknownField
}

// ModeInfo is the protobuf ModeInfo message.
type ModeInfo struct {
	Single        *Single
	Multi         *Multi
	UnknownFields []UnknownField
}

// Single is a single-signature mode descriptor.
type Single struct {
	Mode          SignMode
	UnknownFields []UnknownField
}

// Multi is a multisig mode descriptor.
type Multi struct {
	BitArray      *BitArray
	ModeInfos     []*ModeInfo
	UnknownFields []UnknownField
}

// BitArray is the protobuf BitArray used by multisig.
type BitArray struct {
	Elems         []uint64
	UnknownFields []UnknownField
}

// Fee is the protobuf Fee message.
type Fee struct {
	Amount        []*Coin
	GasLimit      uint64
	Payer         string
	Granter       string
	UnknownFields []UnknownField
}

// Coin is the protobuf Coin message.
type Coin struct {
	Denom         string
	Amount        string
	UnknownFields []UnknownField
}

// Tip is the protobuf Tip message.
type Tip struct {
	Amount        []*Coin
	UnknownFields []UnknownField
}

// AllUnknownFields flattens unknown fields from the complete transaction tree.
func (tx *Tx) AllUnknownFields() []UnknownField {
	if tx == nil {
		return nil
	}
	out := append([]UnknownField(nil), tx.UnknownFields...)
	if tx.Body != nil {
		out = append(out, tx.Body.AllUnknownFields()...)
	}
	if tx.AuthInfo != nil {
		out = append(out, tx.AuthInfo.AllUnknownFields()...)
	}
	return out
}

// AllUnknownFields flattens unknown fields in a TxBody.
func (b *TxBody) AllUnknownFields() []UnknownField {
	if b == nil {
		return nil
	}
	out := append([]UnknownField(nil), b.UnknownFields...)
	for _, msg := range b.Messages {
		out = append(out, msg.AllUnknownFields()...)
	}
	for _, opt := range b.ExtensionOptions {
		out = append(out, opt.AllUnknownFields()...)
	}
	for _, opt := range b.NonCriticalExtensionOptions {
		out = append(out, opt.AllUnknownFields()...)
	}
	return out
}

// AllUnknownFields flattens unknown fields in an Any envelope.
func (a *Any) AllUnknownFields() []UnknownField {
	if a == nil {
		return nil
	}
	return append([]UnknownField(nil), a.UnknownFields...)
}

// AllUnknownFields flattens unknown fields in an AuthInfo.
func (a *AuthInfo) AllUnknownFields() []UnknownField {
	if a == nil {
		return nil
	}
	out := append([]UnknownField(nil), a.UnknownFields...)
	for _, si := range a.SignerInfos {
		out = append(out, si.AllUnknownFields()...)
	}
	if a.Fee != nil {
		out = append(out, a.Fee.AllUnknownFields()...)
	}
	if a.Tip != nil {
		out = append(out, a.Tip.AllUnknownFields()...)
	}
	return out
}

// AllUnknownFields flattens unknown fields in a SignerInfo.
func (s *SignerInfo) AllUnknownFields() []UnknownField {
	if s == nil {
		return nil
	}
	out := append([]UnknownField(nil), s.UnknownFields...)
	if s.PublicKey != nil {
		out = append(out, s.PublicKey.AllUnknownFields()...)
	}
	if s.ModeInfo != nil {
		out = append(out, s.ModeInfo.AllUnknownFields()...)
	}
	return out
}

// AllUnknownFields flattens unknown fields in a ModeInfo.
func (m *ModeInfo) AllUnknownFields() []UnknownField {
	if m == nil {
		return nil
	}
	out := append([]UnknownField(nil), m.UnknownFields...)
	if m.Single != nil {
		out = append(out, m.Single.UnknownFields...)
	}
	if m.Multi != nil {
		out = append(out, m.Multi.AllUnknownFields()...)
	}
	return out
}

// AllUnknownFields flattens unknown fields in a Multi mode.
func (m *Multi) AllUnknownFields() []UnknownField {
	if m == nil {
		return nil
	}
	out := append([]UnknownField(nil), m.UnknownFields...)
	if m.BitArray != nil {
		out = append(out, m.BitArray.UnknownFields...)
	}
	for _, mi := range m.ModeInfos {
		out = append(out, mi.AllUnknownFields()...)
	}
	return out
}

// AllUnknownFields flattens unknown fields in a Fee.
func (f *Fee) AllUnknownFields() []UnknownField {
	if f == nil {
		return nil
	}
	out := append([]UnknownField(nil), f.UnknownFields...)
	for _, coin := range f.Amount {
		out = append(out, coin.AllUnknownFields()...)
	}
	return out
}

// AllUnknownFields flattens unknown fields in a Coin.
func (c *Coin) AllUnknownFields() []UnknownField {
	if c == nil {
		return nil
	}
	return append([]UnknownField(nil), c.UnknownFields...)
}

// AllUnknownFields flattens unknown fields in a Tip.
func (t *Tip) AllUnknownFields() []UnknownField {
	if t == nil {
		return nil
	}
	out := append([]UnknownField(nil), t.UnknownFields...)
	for _, coin := range t.Amount {
		out = append(out, coin.AllUnknownFields()...)
	}
	return out
}

// SignModes returns the sign mode for every signer info. Multisig mode infos
// are flattened. A signer info without a mode reports SignModeUnspecified.
func (tx *Tx) SignModes() []SignMode {
	if tx == nil || tx.AuthInfo == nil {
		return nil
	}
	var modes []SignMode
	for _, si := range tx.AuthInfo.SignerInfos {
		modes = append(modes, si.SignModes()...)
	}
	return modes
}

// SignModes returns the modes carried by one signer info.
func (s *SignerInfo) SignModes() []SignMode {
	if s == nil || s.ModeInfo == nil {
		return []SignMode{SignModeUnspecified}
	}
	modes := s.ModeInfo.SignModes()
	if len(modes) == 0 {
		return []SignMode{SignModeUnspecified}
	}
	return modes
}

// SignModes returns the modes contained in a ModeInfo.
func (m *ModeInfo) SignModes() []SignMode {
	if m == nil {
		return nil
	}
	var modes []SignMode
	if m.Single != nil {
		modes = append(modes, m.Single.Mode)
	}
	if m.Multi != nil {
		for _, nested := range m.Multi.ModeInfos {
			modes = append(modes, nested.SignModes()...)
		}
	}
	return modes
}

// HasSignMode reports whether the transaction contains the given mode.
func (tx *Tx) HasSignMode(mode SignMode) bool {
	for _, candidate := range tx.SignModes() {
		if candidate == mode {
			return true
		}
	}
	return false
}

// UsesLegacyAminoJSON reports whether any signer uses legacy Amino JSON.
func (tx *Tx) UsesLegacyAminoJSON() bool {
	return tx.HasSignMode(SignModeLegacyAminoJSON)
}

// MarshalTx encodes a Tx to protobuf wire bytes. Unknown fields are not
// re-encoded; use ParseTx when round-tripping attacker-controlled bytes.
func MarshalTx(tx *Tx) ([]byte, error) {
	if tx == nil {
		return nil, ErrMalformedTx
	}
	var out []byte
	if tx.Body != nil {
		body, err := MarshalTxBody(tx.Body)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 1, body)
	}
	if tx.AuthInfo != nil {
		auth, err := MarshalAuthInfo(tx.AuthInfo)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 2, auth)
	}
	for _, signature := range tx.Signatures {
		out = appendBytesField(out, 3, signature)
	}
	return out, nil
}

// MarshalTxBody encodes a TxBody to protobuf wire bytes.
func MarshalTxBody(body *TxBody) ([]byte, error) {
	if body == nil {
		return nil, ErrMalformedTx
	}
	var out []byte
	for _, msg := range body.Messages {
		encoded, err := MarshalAny(msg)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 1, encoded)
	}
	if body.Memo != "" {
		out = appendStringField(out, 2, body.Memo)
	}
	if body.TimeoutHeight != 0 {
		out = appendVarintField(out, 3, body.TimeoutHeight)
	}
	for _, opt := range body.ExtensionOptions {
		encoded, err := MarshalAny(opt)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 1023, encoded)
	}
	for _, opt := range body.NonCriticalExtensionOptions {
		encoded, err := MarshalAny(opt)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 2047, encoded)
	}
	return out, nil
}

// MarshalAny encodes an Any envelope.
func MarshalAny(a *Any) ([]byte, error) {
	if a == nil {
		return nil, ErrMalformedTx
	}
	var out []byte
	if a.TypeURL != "" {
		out = appendStringField(out, 1, a.TypeURL)
	}
	if len(a.Value) != 0 {
		out = appendBytesField(out, 2, a.Value)
	}
	return out, nil
}

// MarshalAuthInfo encodes an AuthInfo.
func MarshalAuthInfo(auth *AuthInfo) ([]byte, error) {
	if auth == nil {
		return nil, ErrMalformedTx
	}
	var out []byte
	for _, si := range auth.SignerInfos {
		encoded, err := MarshalSignerInfo(si)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 1, encoded)
	}
	if auth.Fee != nil {
		encoded, err := MarshalFee(auth.Fee)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 2, encoded)
	}
	if auth.Tip != nil {
		encoded, err := MarshalTip(auth.Tip)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 3, encoded)
	}
	return out, nil
}

// MarshalSignerInfo encodes a SignerInfo.
func MarshalSignerInfo(si *SignerInfo) ([]byte, error) {
	if si == nil {
		return nil, ErrMalformedTx
	}
	var out []byte
	if si.PublicKey != nil {
		encoded, err := MarshalAny(si.PublicKey)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 1, encoded)
	}
	if si.ModeInfo != nil {
		encoded, err := MarshalModeInfo(si.ModeInfo)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 2, encoded)
	}
	if si.Sequence != 0 {
		out = appendVarintField(out, 3, si.Sequence)
	}
	return out, nil
}

// MarshalModeInfo encodes a ModeInfo.
func MarshalModeInfo(mi *ModeInfo) ([]byte, error) {
	if mi == nil {
		return nil, ErrMalformedTx
	}
	var out []byte
	if mi.Single != nil {
		single := appendVarintField(nil, 1, uint64(mi.Single.Mode))
		out = appendBytesField(out, 1, single)
	}
	if mi.Multi != nil {
		encoded, err := MarshalMulti(mi.Multi)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 2, encoded)
	}
	return out, nil
}

// MarshalMulti encodes a Multi mode.
func MarshalMulti(multi *Multi) ([]byte, error) {
	if multi == nil {
		return nil, ErrMalformedTx
	}
	var out []byte
	if multi.BitArray != nil {
		encoded, err := MarshalBitArray(multi.BitArray)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 1, encoded)
	}
	for _, nested := range multi.ModeInfos {
		encoded, err := MarshalModeInfo(nested)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 2, encoded)
	}
	return out, nil
}

// MarshalBitArray encodes a BitArray. Elems are written packed.
func MarshalBitArray(bits *BitArray) ([]byte, error) {
	if bits == nil {
		return nil, ErrMalformedTx
	}
	var packed []byte
	for _, elem := range bits.Elems {
		packed = appendVarint(packed, elem)
	}
	if len(packed) == 0 {
		return nil, nil
	}
	return appendBytesField(nil, 1, packed), nil
}

// MarshalFee encodes a Fee.
func MarshalFee(fee *Fee) ([]byte, error) {
	if fee == nil {
		return nil, ErrMalformedTx
	}
	var out []byte
	for _, coin := range fee.Amount {
		encoded, err := MarshalCoin(coin)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 1, encoded)
	}
	if fee.GasLimit != 0 {
		out = appendVarintField(out, 2, fee.GasLimit)
	}
	if fee.Payer != "" {
		out = appendStringField(out, 3, fee.Payer)
	}
	if fee.Granter != "" {
		out = appendStringField(out, 4, fee.Granter)
	}
	return out, nil
}

// MarshalCoin encodes a Coin.
func MarshalCoin(coin *Coin) ([]byte, error) {
	if coin == nil {
		return nil, ErrMalformedTx
	}
	var out []byte
	if coin.Denom != "" {
		out = appendStringField(out, 1, coin.Denom)
	}
	if coin.Amount != "" {
		out = appendStringField(out, 2, coin.Amount)
	}
	return out, nil
}

// MarshalTip encodes a Tip.
func MarshalTip(tip *Tip) ([]byte, error) {
	if tip == nil {
		return nil, ErrMalformedTx
	}
	var out []byte
	for _, coin := range tip.Amount {
		encoded, err := MarshalCoin(coin)
		if err != nil {
			return nil, err
		}
		out = appendBytesField(out, 1, encoded)
	}
	return out, nil
}
