package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"math"
)

const (
	// SighashAll is the only sighash type accepted by the policy.
	SighashAll uint32 = 1

	CodeSighashNotAll          = "sighash_not_all"
	CodeMissingNonWitnessUTXO  = "missing_non_witness_utxo"
	CodeNonWitnessUTXOMismatch = "non_witness_utxo_mismatch"
	CodePrevoutUnavailable     = "prevout_unavailable"
	CodeAmountMismatch         = "amount_mismatch"
	CodeScriptMismatch         = "script_mismatch"
	CodeFeeLimitExceeded       = "fee_limit_exceeded"
	CodeNegativeFee            = "negative_fee"
	CodeDerivationFailed       = "derivation_failed"
	CodeInvalidConfig          = "invalid_config"
	CodeMalformedPSBT          = "malformed_psbt"
)

var (
	// ErrNotMine is returned by KeyDeriver when the supplied master
	// fingerprint is not controlled by this signer.
	ErrNotMine = errors.New("netsec: key is not controlled by this signer")
	// ErrPrevoutNotFound is returned by ChainSource when the outpoint is not
	// present in its trusted view of the chain.
	ErrPrevoutNotFound = errors.New("netsec: prevout not found")

	ErrSighashNotAll          = errors.New("netsec: sighash must be SIGHASH_ALL")
	ErrMissingNonWitnessUTXO  = errors.New("netsec: missing non-witness utxo")
	ErrNonWitnessUTXOMismatch = errors.New("netsec: non-witness utxo does not match prevout")
	ErrPrevoutUnavailable     = errors.New("netsec: prevout evidence is unavailable")
	ErrAmountMismatch         = errors.New("netsec: input amount does not match trusted source")
	ErrScriptMismatch         = errors.New("netsec: input script does not match trusted source")
	ErrFeeLimitExceeded       = errors.New("netsec: fee exceeds configured limit")
	ErrNegativeFee            = errors.New("netsec: inputs do not cover outputs")
	ErrDerivationFailed       = errors.New("netsec: key derivation failed")
	ErrInvalidConfig          = errors.New("netsec: invalid policy config")
)

// KeyDeriver derives the scriptPubKey for a BIP32 path under the supplied
// master fingerprint. The signer, not the PSBT, owns this operation. A
// deriver that does not control the fingerprint must return ErrNotMine.
type KeyDeriver func(masterFingerprint [4]byte, path []uint32) (scriptPubKey []byte, err error)

// ChainSource returns the trusted amount and scriptPubKey for a prevout.
// TxID is in display order, matching OutPoint.TxID. Return
// ErrPrevoutNotFound when the outpoint is unknown.
type ChainSource func(txid [32]byte, vout uint32) (amountSat int64, scriptPubKey []byte, err error)

// Config configures a policy. MaxFeeSat is an inclusive absolute cap.
type Config struct {
	KeyDeriver  KeyDeriver
	ChainSource ChainSource
	MaxFeeSat   int64
}

// Policy decides which PSBT inputs may be signed and which outputs are
// change. It never trusts PSBT-supplied amounts over ChainSource.
type Policy struct {
	KeyDeriver  KeyDeriver
	ChainSource ChainSource
	MaxFeeSat   int64
}

// NewPolicy constructs a policy.
func NewPolicy(derive KeyDeriver, source ChainSource, maxFeeSat int64) *Policy {
	return &Policy{KeyDeriver: derive, ChainSource: source, MaxFeeSat: maxFeeSat}
}

// NewPolicyFromConfig constructs a policy from Config.
func NewPolicyFromConfig(cfg Config) *Policy {
	return &Policy{KeyDeriver: cfg.KeyDeriver, ChainSource: cfg.ChainSource, MaxFeeSat: cfg.MaxFeeSat}
}

// PolicyError is a deterministic pre-signing rejection. Errors.Is works
// against the sentinel error in Err.
type PolicyError struct {
	Code        string
	InputIndex  int
	OutputIndex int
	Detail      string
	Err         error
}

func (e *PolicyError) Error() string {
	location := ""
	switch {
	case e.InputIndex >= 0:
		location = fmt.Sprintf(" input %d", e.InputIndex)
	case e.OutputIndex >= 0:
		location = fmt.Sprintf(" output %d", e.OutputIndex)
	}
	if e.Detail == "" {
		return fmt.Sprintf("%v%s", e.Err, location)
	}
	return fmt.Sprintf("%v%s: %s", e.Err, location, e.Detail)
}

func (e *PolicyError) Unwrap() error {
	return e.Err
}

func policyError(code string, base error, input, output int, detail string) *PolicyError {
	return &PolicyError{
		Code:        code,
		InputIndex:  input,
		OutputIndex: output,
		Detail:      detail,
		Err:         base,
	}
}

// InputDecision is the policy result for one input. Sign is false for
// foreign inputs; they are skipped rather than treated as controlled.
type InputDecision struct {
	Index        int
	Sign         bool
	Owned        bool
	Segwit       bool
	AmountSat    int64
	ScriptPubKey []byte
	Fingerprint  [4]byte
	Path         []uint32
	Reason       string
}

// OutputDecision is the policy result for one output. Show is true for every
// output that is not proven change and must be presented to the user.
type OutputDecision struct {
	Index        int
	Change       bool
	Show         bool
	AmountSat    int64
	ScriptPubKey []byte
	Fingerprint  [4]byte
	Path         []uint32
	Reason       string
}

// Evaluation is the successful pre-signing decision set.
type Evaluation struct {
	FeeSat         int64
	Inputs         []InputDecision
	Outputs        []OutputDecision
	SignInputs     []int
	DisplayOutputs []int
}

// Evaluate applies the complete pre-signing policy. Foreign inputs are
// reported with Sign=false; every non-change output is reported in
// DisplayOutputs. Deterministic policy violations return *PolicyError.
func (p *Policy) Evaluate(psbt *PSBT) (*Evaluation, error) {
	if p == nil || p.KeyDeriver == nil {
		return nil, policyError(CodeInvalidConfig, ErrInvalidConfig, -1, -1, "key deriver is required")
	}
	if p.MaxFeeSat < 0 {
		return nil, policyError(CodeInvalidConfig, ErrInvalidConfig, -1, -1, "max fee must be non-negative")
	}
	if psbt == nil || psbt.UnsignedTx == nil {
		return nil, policyError(CodeMalformedPSBT, ErrMalformedPSBT, -1, -1, "missing unsigned transaction")
	}
	if len(psbt.Inputs) != len(psbt.UnsignedTx.Inputs) {
		return nil, policyError(CodeMalformedPSBT, ErrMalformedPSBT, -1, -1, "input map count does not match unsigned transaction")
	}
	if len(psbt.Outputs) != len(psbt.UnsignedTx.Outputs) {
		return nil, policyError(CodeMalformedPSBT, ErrMalformedPSBT, -1, -1, "output map count does not match unsigned transaction")
	}

	for i := range psbt.Inputs {
		in := &psbt.Inputs[i]
		if in.HasSighashType && in.SighashType != SighashAll {
			return nil, policyError(CodeSighashNotAll, ErrSighashNotAll, i, -1,
				fmt.Sprintf("sighash type is 0x%08x", in.SighashType))
		}
	}

	ev := &Evaluation{
		Inputs:  make([]InputDecision, 0, len(psbt.Inputs)),
		Outputs: make([]OutputDecision, 0, len(psbt.Outputs)),
	}

	var totalIn int64
	for i := range psbt.Inputs {
		in := &psbt.Inputs[i]
		txIn := &psbt.UnsignedTx.Inputs[i]
		prevout, segwit, err := p.resolveInput(i, in, txIn)
		if err != nil {
			return nil, err
		}
		totalIn, err = addSatoshis(totalIn, prevout.AmountSat)
		if err != nil {
			return nil, policyError(CodeAmountMismatch, ErrAmountMismatch, i, -1, err.Error())
		}

		owned, deriv, err := p.matchOwnership(in.BIP32Derivations, prevout.ScriptPubKey)
		if err != nil {
			return nil, err
		}

		decision := InputDecision{
			Index:        i,
			Sign:         owned,
			Owned:        owned,
			Segwit:       segwit,
			AmountSat:    prevout.AmountSat,
			ScriptPubKey: cloneBytes(prevout.ScriptPubKey),
		}
		if owned {
			decision.Fingerprint = deriv.Fingerprint
			decision.Path = clonePath(deriv.Path)
			decision.Reason = "scriptPubKey matches local key derivation"
			ev.SignInputs = append(ev.SignInputs, i)
		} else {
			decision.Reason = "input is not controlled by this signer; skipped"
		}
		ev.Inputs = append(ev.Inputs, decision)
	}

	var totalOut int64
	for i := range psbt.Outputs {
		out := &psbt.UnsignedTx.Outputs[i]
		psbtOut := &psbt.Outputs[i]
		change, deriv, err := p.matchOwnership(psbtOut.BIP32Derivations, out.ScriptPubKey)
		if err != nil {
			return nil, policyError(CodeDerivationFailed, ErrDerivationFailed, -1, i, err.Error())
		}

		var errAdd error
		totalOut, errAdd = addSatoshis(totalOut, out.ValueSat)
		if errAdd != nil {
			return nil, policyError(CodeAmountMismatch, ErrAmountMismatch, -1, i, errAdd.Error())
		}

		decision := OutputDecision{
			Index:        i,
			Change:       change,
			Show:         !change,
			AmountSat:    out.ValueSat,
			ScriptPubKey: cloneBytes(out.ScriptPubKey),
		}
		if change {
			decision.Fingerprint = deriv.Fingerprint
			decision.Path = clonePath(deriv.Path)
			decision.Reason = "output script matches local change derivation"
		} else {
			decision.Reason = "external output; show to user"
			ev.DisplayOutputs = append(ev.DisplayOutputs, i)
		}
		ev.Outputs = append(ev.Outputs, decision)
	}

	if totalOut > totalIn {
		return nil, policyError(CodeNegativeFee, ErrNegativeFee, -1, -1,
			fmt.Sprintf("inputs %d < outputs %d", totalIn, totalOut))
	}
	fee := totalIn - totalOut
	if fee > p.MaxFeeSat {
		return nil, policyError(CodeFeeLimitExceeded, ErrFeeLimitExceeded, -1, -1,
			fmt.Sprintf("fee %d > limit %d", fee, p.MaxFeeSat))
	}
	ev.FeeSat = fee
	return ev, nil
}

// Check is an alias for Evaluate.
func (p *Policy) Check(psbt *PSBT) (*Evaluation, error) {
	return p.Evaluate(psbt)
}

// CheckPSBT applies Config to a parsed PSBT.
func CheckPSBT(psbt *PSBT, cfg Config) (*Evaluation, error) {
	return NewPolicyFromConfig(cfg).Evaluate(psbt)
}

func (p *Policy) resolveInput(index int, in *Input, txIn *TxIn) (Prevout, bool, error) {
	prev := txIn.PreviousOutPoint
	var source Prevout
	haveSource := false
	if p.ChainSource != nil {
		var err error
		source, err = p.lookupPrevout(prev)
		switch {
		case err == nil:
			haveSource = true
		case errors.Is(err, ErrPrevoutNotFound):
			// A non-witness UTXO can still bind a legacy prevout below.
		default:
			return Prevout{}, false, wrapSourceError(err, index)
		}
	}

	var nwu Prevout
	haveNWU := in.NonWitnessUTXO != nil
	if haveNWU {
		var err error
		nwu, err = p.verifyNonWitnessUTXO(in, prev, index)
		if err != nil {
			return Prevout{}, false, err
		}
	}

	// Classification must use evidence that is bound to the prevout, never
	// PSBT-provided redeem/witness scripts on their own.
	var trustedScript []byte
	switch {
	case haveSource:
		trustedScript = source.ScriptPubKey
	case haveNWU:
		trustedScript = nwu.ScriptPubKey
	case in.WitnessUTXO != nil:
		trustedScript = in.WitnessUTXO.ScriptPubKey
	}
	segwit := isSegwitScript(trustedScript, in.RedeemScript)

	if segwit {
		if !haveSource {
			return Prevout{}, true, policyError(CodePrevoutUnavailable, ErrPrevoutUnavailable, index, -1,
				"chain source is required for segwit inputs")
		}
		if in.WitnessUTXO != nil {
			if in.WitnessUTXO.ValueSat != source.AmountSat {
				return Prevout{}, true, policyError(CodeAmountMismatch, ErrAmountMismatch, index, -1,
					fmt.Sprintf("psbt witness amount %d != chain source %d", in.WitnessUTXO.ValueSat, source.AmountSat))
			}
			if !bytes.Equal(in.WitnessUTXO.ScriptPubKey, source.ScriptPubKey) {
				return Prevout{}, true, policyError(CodeScriptMismatch, ErrScriptMismatch, index, -1,
					"psbt witness script does not match chain source")
			}
		}
		if haveNWU {
			if err := comparePrevout(nwu, source); err != nil {
				return Prevout{}, true, compareError(index, err)
			}
		}
		return source, true, nil
	}

	if !haveNWU {
		return Prevout{}, false, policyError(CodeMissingNonWitnessUTXO, ErrMissingNonWitnessUTXO, index, -1,
			"non-segwit input requires non_witness_utxo")
	}
	if haveSource {
		if err := comparePrevout(nwu, source); err != nil {
			return Prevout{}, false, compareError(index, err)
		}
	}
	return nwu, false, nil
}

type Prevout struct {
	AmountSat    int64
	ScriptPubKey []byte
}

func (p *Policy) lookupPrevout(prev OutPoint) (Prevout, error) {
	amount, script, err := p.ChainSource(prev.TxID, prev.Index)
	if err != nil {
		return Prevout{}, err
	}
	if amount < 0 {
		return Prevout{}, fmt.Errorf("chain source returned negative amount %d", amount)
	}
	return Prevout{AmountSat: amount, ScriptPubKey: cloneBytes(script)}, nil
}

func wrapSourceError(err error, input int) error {
	if errors.Is(err, ErrPrevoutNotFound) {
		return policyError(CodePrevoutUnavailable, ErrPrevoutUnavailable, input, -1, "prevout not found in chain source")
	}
	return policyError(CodePrevoutUnavailable, ErrPrevoutUnavailable, input, -1, err.Error())
}

func (p *Policy) verifyNonWitnessUTXO(in *Input, prev OutPoint, index int) (Prevout, error) {
	tx, err := parseTransaction(in.NonWitnessUTXO, true)
	if err != nil {
		return Prevout{}, policyError(CodeNonWitnessUTXOMismatch, ErrNonWitnessUTXOMismatch, index, -1,
			fmt.Sprintf("invalid non_witness_utxo transaction: %v", err))
	}
	txid := tx.TxID()
	if txid != prev.TxID {
		return Prevout{}, policyError(CodeNonWitnessUTXOMismatch, ErrNonWitnessUTXOMismatch, index, -1,
			"non_witness_utxo txid does not equal prevout txid")
	}
	if uint64(prev.Index) >= uint64(len(tx.Outputs)) {
		return Prevout{}, policyError(CodeNonWitnessUTXOMismatch, ErrNonWitnessUTXOMismatch, index, -1,
			fmt.Sprintf("prevout index %d is outside non_witness_utxo", prev.Index))
	}
	out := tx.Outputs[prev.Index]
	return Prevout{AmountSat: out.ValueSat, ScriptPubKey: cloneBytes(out.ScriptPubKey)}, nil
}

func comparePrevout(a, b Prevout) error {
	if a.AmountSat != b.AmountSat {
		return fmt.Errorf("%w: amount %d != trusted %d", ErrAmountMismatch, a.AmountSat, b.AmountSat)
	}
	if !bytes.Equal(a.ScriptPubKey, b.ScriptPubKey) {
		return fmt.Errorf("%w: scriptPubKey does not match trusted source", ErrScriptMismatch)
	}
	return nil
}

func compareError(index int, err error) error {
	switch {
	case errors.Is(err, ErrScriptMismatch):
		return policyError(CodeScriptMismatch, ErrScriptMismatch, index, -1, err.Error())
	case errors.Is(err, ErrAmountMismatch):
		return policyError(CodeAmountMismatch, ErrAmountMismatch, index, -1, err.Error())
	default:
		return policyError(CodePrevoutUnavailable, ErrPrevoutUnavailable, index, -1, err.Error())
	}
}

func (p *Policy) matchOwnership(derivations []BIP32Derivation, scriptPubKey []byte) (bool, BIP32Derivation, error) {
	if len(scriptPubKey) == 0 {
		return false, BIP32Derivation{}, nil
	}
	for i := range derivations {
		deriv := derivations[i]
		script, err := p.KeyDeriver(deriv.Fingerprint, deriv.Path)
		if errors.Is(err, ErrNotMine) {
			continue
		}
		if err != nil {
			return false, BIP32Derivation{}, policyError(CodeDerivationFailed, ErrDerivationFailed, -1, -1, err.Error())
		}
		if len(script) == 0 {
			continue
		}
		if bytes.Equal(script, scriptPubKey) {
			return true, deriv, nil
		}
	}
	return false, BIP32Derivation{}, nil
}

func isSegwitScript(script, redeemScript []byte) bool {
	if isWitnessProgram(script) {
		return true
	}
	if isP2SH(script) && p2shRedeemMatches(script, redeemScript) && isWitnessProgram(redeemScript) {
		return true
	}
	return false
}

func p2shRedeemMatches(script, redeemScript []byte) bool {
	if !isP2SH(script) || len(redeemScript) == 0 {
		return false
	}
	hash := hash160(redeemScript)
	return bytes.Equal(hash[:], script[2:22])
}

func isP2SH(script []byte) bool {
	return len(script) == 23 &&
		script[0] == 0xa9 && script[1] == 0x14 && script[22] == 0x87
}

func isWitnessProgram(script []byte) bool {
	if len(script) < 4 || len(script) > 42 {
		return false
	}
	versionOp := script[0]
	if versionOp != 0x00 && (versionOp < 0x51 || versionOp > 0x60) {
		return false
	}
	programLen := int(script[1])
	if programLen < 2 || programLen > 40 {
		return false
	}
	return len(script) == 2+programLen
}

func addSatoshis(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, fmt.Errorf("negative satoshi amount")
	}
	if b > math.MaxInt64-a {
		return 0, fmt.Errorf("satoshi amount overflow")
	}
	return a + b, nil
}

func clonePath(path []uint32) []uint32 {
	if path == nil {
		return nil
	}
	out := make([]uint32, len(path))
	copy(out, path)
	return out
}
