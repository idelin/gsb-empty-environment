package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

var testFingerprint = [4]byte{0xde, 0xad, 0xbe, 0xef}

var (
	pathInput  = []uint32{0x80000054, 0x80000000, 0x80000000, 0, 10}
	pathChange = []uint32{0x80000054, 0x80000000, 0x80000000, 0, 11}
	pathLegacy = []uint32{0x8000002c, 0x80000000, 0x80000000, 0, 20}
	pathNested = []uint32{0x80000031, 0x80000000, 0x80000000, 0, 30}
)

func TestEvaluateSignsOnlyOwnedInputAndRecognizesExactChange(t *testing.T) {
	ownScript := p2wpkh(0x11)
	changeScript := p2wpkh(0x22)
	externalScript := p2wpkh(0x33)
	prev := outpointWithFill(0x40)

	tx := spendTransaction([]OutPoint{prev}, []TxOut{
		{ValueSat: 70_000, ScriptPubKey: externalScript},
		{ValueSat: 25_000, ScriptPubKey: changeScript},
	})
	inputMaps := [][]keyValue{{
		witnessUTXOField(TxOut{ValueSat: 100_000, ScriptPubKey: ownScript}),
		bip32InputField(pathInput),
	}}
	outputMaps := [][]keyValue{
		nil,
		{bip32OutputField(pathChange)},
	}

	psbt := parseTestPSBT(t, buildTestPSBT(t, tx, inputMaps, outputMaps))
	policy := NewPolicy(testDeriver, testSource(map[string]Prevout{
		sourceKey(prev): {AmountSat: 100_000, ScriptPubKey: ownScript},
	}), 5_000)

	ev, err := policy.Evaluate(psbt)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if ev.FeeSat != 5_000 {
		t.Fatalf("fee = %d, want 5000", ev.FeeSat)
	}
	if !reflect.DeepEqual(ev.SignInputs, []int{0}) {
		t.Fatalf("SignInputs = %v, want [0]", ev.SignInputs)
	}
	if !ev.Inputs[0].Sign || !ev.Inputs[0].Owned || !ev.Inputs[0].Segwit {
		t.Fatalf("input decision = %+v, want owned segwit sign", ev.Inputs[0])
	}
	if ev.Outputs[0].Change || !ev.Outputs[0].Show {
		t.Fatalf("external output = %+v, want displayed non-change", ev.Outputs[0])
	}
	if !ev.Outputs[1].Change || ev.Outputs[1].Show {
		t.Fatalf("change output = %+v, want change not displayed", ev.Outputs[1])
	}
	if !reflect.DeepEqual(ev.DisplayOutputs, []int{0}) {
		t.Fatalf("DisplayOutputs = %v, want [0]", ev.DisplayOutputs)
	}
}

func TestRejectsInflatedSegwitAmount(t *testing.T) {
	ownScript := p2wpkh(0x11)
	prev := outpointWithFill(0x41)
	tx := spendTransaction([]OutPoint{prev}, []TxOut{
		{ValueSat: 60_000, ScriptPubKey: p2wpkh(0x33)},
	})
	inputMaps := [][]keyValue{{
		witnessUTXOField(TxOut{ValueSat: 120_000, ScriptPubKey: ownScript}),
		bip32InputField(pathInput),
	}}
	psbt := parseTestPSBT(t, buildTestPSBT(t, tx, inputMaps, [][]keyValue{nil}))
	policy := NewPolicy(testDeriver, testSource(map[string]Prevout{
		sourceKey(prev): {AmountSat: 100_000, ScriptPubKey: ownScript},
	}), 30_000)

	_, err := policy.Evaluate(psbt)
	if !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("error = %v, want ErrAmountMismatch", err)
	}
	assertPolicyCode(t, err, CodeAmountMismatch)
}

func TestForgedChangeIsNeverChange(t *testing.T) {
	ownScript := p2wpkh(0x11)
	attackerScript := p2wpkh(0x44)
	prev := outpointWithFill(0x42)
	tx := spendTransaction([]OutPoint{prev}, []TxOut{
		{ValueSat: 70_000, ScriptPubKey: p2wpkh(0x33)},
		{ValueSat: 25_000, ScriptPubKey: attackerScript},
	})
	inputMaps := [][]keyValue{{
		witnessUTXOField(TxOut{ValueSat: 100_000, ScriptPubKey: ownScript}),
		bip32InputField(pathInput),
	}}
	outputMaps := [][]keyValue{
		nil,
		{bip32OutputField(pathChange)},
	}
	psbt := parseTestPSBT(t, buildTestPSBT(t, tx, inputMaps, outputMaps))
	policy := NewPolicy(testDeriver, testSource(map[string]Prevout{
		sourceKey(prev): {AmountSat: 100_000, ScriptPubKey: ownScript},
	}), 5_000)

	ev, err := policy.Evaluate(psbt)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if ev.Outputs[1].Change || !ev.Outputs[1].Show {
		t.Fatalf("forged change decision = %+v, want external/displayed", ev.Outputs[1])
	}
	if !reflect.DeepEqual(ev.DisplayOutputs, []int{0, 1}) {
		t.Fatalf("DisplayOutputs = %v, want [0 1]", ev.DisplayOutputs)
	}
}

func TestForeignInputIsSkippedEvenWithSpoofedDerivation(t *testing.T) {
	ownScript := p2wpkh(0x11)
	foreignScript := p2wpkh(0x55)
	prev0 := outpointWithFill(0x43)
	prev1 := outpointWithFill(0x44)
	tx := spendTransaction([]OutPoint{prev0, prev1}, []TxOut{
		{ValueSat: 140_000, ScriptPubKey: p2wpkh(0x33)},
	})
	inputMaps := [][]keyValue{
		{
			witnessUTXOField(TxOut{ValueSat: 100_000, ScriptPubKey: ownScript}),
			bip32InputField(pathInput),
		},
		{
			witnessUTXOField(TxOut{ValueSat: 50_000, ScriptPubKey: foreignScript}),
			// The attacker declares a local path, but the trusted
			// scriptPubKey does not equal the derived script.
			bip32InputField(pathInput),
		},
	}
	psbt := parseTestPSBT(t, buildTestPSBT(t, tx, inputMaps, [][]keyValue{nil}))
	policy := NewPolicy(testDeriver, testSource(map[string]Prevout{
		sourceKey(prev0): {AmountSat: 100_000, ScriptPubKey: ownScript},
		sourceKey(prev1): {AmountSat: 50_000, ScriptPubKey: foreignScript},
	}), 10_000)

	ev, err := policy.Evaluate(psbt)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !reflect.DeepEqual(ev.SignInputs, []int{0}) {
		t.Fatalf("SignInputs = %v, want [0]", ev.SignInputs)
	}
	if ev.Inputs[1].Sign || ev.Inputs[1].Owned {
		t.Fatalf("foreign input decision = %+v, want skipped", ev.Inputs[1])
	}
}

func TestRejectsNonAllSighash(t *testing.T) {
	ownScript := p2wpkh(0x11)
	prev := outpointWithFill(0x45)
	tx := spendTransaction([]OutPoint{prev}, []TxOut{
		{ValueSat: 90_000, ScriptPubKey: p2wpkh(0x33)},
	})
	inputMaps := [][]keyValue{{
		witnessUTXOField(TxOut{ValueSat: 100_000, ScriptPubKey: ownScript}),
		bip32InputField(pathInput),
		sighashField(SighashAll | 0x80),
	}}
	psbt := parseTestPSBT(t, buildTestPSBT(t, tx, inputMaps, [][]keyValue{nil}))
	policy := NewPolicy(testDeriver, testSource(map[string]Prevout{
		sourceKey(prev): {AmountSat: 100_000, ScriptPubKey: ownScript},
	}), 10_000)

	_, err := policy.Evaluate(psbt)
	if !errors.Is(err, ErrSighashNotAll) {
		t.Fatalf("error = %v, want ErrSighashNotAll", err)
	}
	assertPolicyCode(t, err, CodeSighashNotAll)
}

func TestRejectsFeeOverLimit(t *testing.T) {
	ownScript := p2wpkh(0x11)
	prev := outpointWithFill(0x46)
	tx := spendTransaction([]OutPoint{prev}, []TxOut{
		{ValueSat: 94_000, ScriptPubKey: p2wpkh(0x33)},
	})
	inputMaps := [][]keyValue{{
		witnessUTXOField(TxOut{ValueSat: 100_000, ScriptPubKey: ownScript}),
		bip32InputField(pathInput),
	}}
	psbt := parseTestPSBT(t, buildTestPSBT(t, tx, inputMaps, [][]keyValue{nil}))
	policy := NewPolicy(testDeriver, testSource(map[string]Prevout{
		sourceKey(prev): {AmountSat: 100_000, ScriptPubKey: ownScript},
	}), 5_000)

	_, err := policy.Evaluate(psbt)
	if !errors.Is(err, ErrFeeLimitExceeded) {
		t.Fatalf("error = %v, want ErrFeeLimitExceeded", err)
	}
	assertPolicyCode(t, err, CodeFeeLimitExceeded)
}

func TestLegacyInputRequiresNonWitnessUTXO(t *testing.T) {
	ownScript := p2pkh(0x11)
	prev := outpointWithFill(0x47)
	tx := spendTransaction([]OutPoint{prev}, []TxOut{
		{ValueSat: 90_000, ScriptPubKey: p2wpkh(0x33)},
	})
	inputMaps := [][]keyValue{{
		witnessUTXOField(TxOut{ValueSat: 100_000, ScriptPubKey: ownScript}),
		bip32InputField(pathLegacy),
	}}
	psbt := parseTestPSBT(t, buildTestPSBT(t, tx, inputMaps, [][]keyValue{nil}))
	policy := NewPolicy(testDeriver, testSource(map[string]Prevout{
		sourceKey(prev): {AmountSat: 100_000, ScriptPubKey: ownScript},
	}), 10_000)

	_, err := policy.Evaluate(psbt)
	if !errors.Is(err, ErrMissingNonWitnessUTXO) {
		t.Fatalf("error = %v, want ErrMissingNonWitnessUTXO", err)
	}
	assertPolicyCode(t, err, CodeMissingNonWitnessUTXO)
}

func TestRejectsNonWitnessUTXOTxIDMismatch(t *testing.T) {
	ownScript := p2pkh(0x11)
	prevTx := makePreviousTransaction(100_000, ownScript, 0x48)
	wrongPrev := outpointWithFill(0x49)
	tx := spendTransaction([]OutPoint{wrongPrev}, []TxOut{
		{ValueSat: 90_000, ScriptPubKey: p2wpkh(0x33)},
	})
	inputMaps := [][]keyValue{{
		nonWitnessUTXOField(prevTx),
		bip32InputField(pathLegacy),
	}}
	psbt := parseTestPSBT(t, buildTestPSBT(t, tx, inputMaps, [][]keyValue{nil}))
	policy := NewPolicy(testDeriver, nil, 10_000)

	_, err := policy.Evaluate(psbt)
	if !errors.Is(err, ErrNonWitnessUTXOMismatch) {
		t.Fatalf("error = %v, want ErrNonWitnessUTXOMismatch", err)
	}
	assertPolicyCode(t, err, CodeNonWitnessUTXOMismatch)
}

func TestValidLegacyInputFromNonWitnessUTXO(t *testing.T) {
	ownScript := p2pkh(0x11)
	prevTx := makePreviousTransaction(100_000, ownScript, 0x4a)
	prev := OutPoint{TxID: prevTx.TxID(), Index: 0}
	tx := spendTransaction([]OutPoint{prev}, []TxOut{
		{ValueSat: 90_000, ScriptPubKey: p2wpkh(0x33)},
	})
	inputMaps := [][]keyValue{{
		nonWitnessUTXOField(prevTx),
		bip32InputField(pathLegacy),
	}}
	psbt := parseTestPSBT(t, buildTestPSBT(t, tx, inputMaps, [][]keyValue{nil}))
	policy := NewPolicy(testDeriver, nil, 10_000)

	ev, err := policy.Evaluate(psbt)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if ev.FeeSat != 10_000 || !reflect.DeepEqual(ev.SignInputs, []int{0}) {
		t.Fatalf("evaluation = %+v, want fee 10000 and sign [0]", ev)
	}
	if ev.Inputs[0].Segwit {
		t.Fatalf("legacy input classified as segwit: %+v", ev.Inputs[0])
	}
}

func TestNestedP2SHWitnessInput(t *testing.T) {
	redeemScript := p2wpkh(0x66)
	ownScript := p2shScript(redeemScript)
	prev := outpointWithFill(0x4b)
	tx := spendTransaction([]OutPoint{prev}, []TxOut{
		{ValueSat: 95_000, ScriptPubKey: p2wpkh(0x33)},
	})
	inputMaps := [][]keyValue{{
		witnessUTXOField(TxOut{ValueSat: 100_000, ScriptPubKey: ownScript}),
		bip32InputField(pathNested),
		{key: []byte{psbtInRedeemScript}, value: redeemScript},
	}}
	psbt := parseTestPSBT(t, buildTestPSBT(t, tx, inputMaps, [][]keyValue{nil}))
	policy := NewPolicy(testDeriver, testSource(map[string]Prevout{
		sourceKey(prev): {AmountSat: 100_000, ScriptPubKey: ownScript},
	}), 5_000)

	ev, err := policy.Evaluate(psbt)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !ev.Inputs[0].Segwit || !ev.Inputs[0].Sign {
		t.Fatalf("nested p2sh input decision = %+v, want owned segwit", ev.Inputs[0])
	}
}

func TestRedeemScriptCannotDowngradeLegacyEvidenceRequirement(t *testing.T) {
	ownScript := p2pkh(0x11)
	attackerRedeem := p2wpkh(0x77)
	prev := outpointWithFill(0x4c)
	tx := spendTransaction([]OutPoint{prev}, []TxOut{
		{ValueSat: 90_000, ScriptPubKey: p2wpkh(0x33)},
	})
	inputMaps := [][]keyValue{{
		witnessUTXOField(TxOut{ValueSat: 100_000, ScriptPubKey: ownScript}),
		bip32InputField(pathLegacy),
		{key: []byte{psbtInRedeemScript}, value: attackerRedeem},
	}}
	psbt := parseTestPSBT(t, buildTestPSBT(t, tx, inputMaps, [][]keyValue{nil}))
	policy := NewPolicy(testDeriver, testSource(map[string]Prevout{
		sourceKey(prev): {AmountSat: 100_000, ScriptPubKey: ownScript},
	}), 10_000)

	_, err := policy.Evaluate(psbt)
	if !errors.Is(err, ErrMissingNonWitnessUTXO) {
		t.Fatalf("error = %v, want ErrMissingNonWitnessUTXO", err)
	}
}

func TestRIPEMD160Vectors(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", "9c1185a5c5e9fc54612808977ee8f548b2258d31"},
		{"abc", "8eb208f7e05d987a9b044a8e98c6b087f15a0bfc"},
	}
	for _, tc := range cases {
		got := fmt.Sprintf("%x", ripemd160Sum([]byte(tc.input)))
		if got != tc.want {
			t.Fatalf("ripemd160(%q) = %s, want %s", tc.input, got, tc.want)
		}
	}
}

func testDeriver(fingerprint [4]byte, path []uint32) ([]byte, error) {
	if fingerprint != testFingerprint || len(path) == 0 {
		return nil, ErrNotMine
	}
	switch path[len(path)-1] {
	case 10:
		return p2wpkh(0x11), nil
	case 11:
		return p2wpkh(0x22), nil
	case 20:
		return p2pkh(0x11), nil
	case 30:
		return p2shScript(p2wpkh(0x66)), nil
	default:
		return nil, ErrNotMine
	}
}

func testSource(prevouts map[string]Prevout) ChainSource {
	return func(txid [32]byte, vout uint32) (int64, []byte, error) {
		prevout, ok := prevouts[fmt.Sprintf("%x:%d", txid[:], vout)]
		if !ok {
			return 0, nil, ErrPrevoutNotFound
		}
		return prevout.AmountSat, cloneBytes(prevout.ScriptPubKey), nil
	}
}

func sourceKey(prev OutPoint) string {
	return fmt.Sprintf("%x:%d", prev.TxID[:], prev.Index)
}

func outpointWithFill(fill byte) OutPoint {
	var txid [32]byte
	for i := range txid {
		txid[i] = fill + byte(i)
	}
	return OutPoint{TxID: txid, Index: 1}
}

func spendTransaction(prevouts []OutPoint, outputs []TxOut) *Tx {
	tx := &Tx{Version: 2, Outputs: outputs}
	for _, prev := range prevouts {
		tx.Inputs = append(tx.Inputs, TxIn{
			PreviousOutPoint: prev,
			Sequence:         0xffffffff,
		})
	}
	return tx
}

func makePreviousTransaction(value int64, script []byte, fill byte) *Tx {
	return &Tx{
		Version: 2,
		Inputs: []TxIn{{
			PreviousOutPoint: outpointWithFill(fill),
			Sequence:         0xffffffff,
		}},
		Outputs: []TxOut{{
			ValueSat:     value,
			ScriptPubKey: script,
		}},
	}
}

func p2wpkh(hashFill byte) []byte {
	hash := bytes.Repeat([]byte{hashFill}, 20)
	return append([]byte{0x00, 0x14}, hash...)
}

func p2pkh(hashFill byte) []byte {
	hash := bytes.Repeat([]byte{hashFill}, 20)
	return append(append([]byte{0x76, 0xa9, 0x14}, hash...), 0x88, 0xac)
}

func p2shScript(redeemScript []byte) []byte {
	hash := hash160(redeemScript)
	return append(append([]byte{0xa9, 0x14}, hash[:]...), 0x87)
}

type keyValue struct {
	key   []byte
	value []byte
}

func buildTestPSBT(t *testing.T, tx *Tx, inputMaps, outputMaps [][]keyValue) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write(psbtMagic)

	writeTestMap(&buf, []keyValue{{
		key:   []byte{psbtGlobalUnsignedTx},
		value: tx.Bytes(false),
	}})
	for _, fields := range inputMaps {
		writeTestMap(&buf, fields)
	}
	for _, fields := range outputMaps {
		writeTestMap(&buf, fields)
	}
	return buf.Bytes()
}

func writeTestMap(buf *bytes.Buffer, fields []keyValue) {
	for _, field := range fields {
		writeCompact(buf, uint64(len(field.key)))
		buf.Write(field.key)
		writeCompact(buf, uint64(len(field.value)))
		buf.Write(field.value)
	}
	buf.WriteByte(0)
}

func witnessUTXOField(out TxOut) keyValue {
	var value bytes.Buffer
	var amount [8]byte
	binary.LittleEndian.PutUint64(amount[:], uint64(out.ValueSat))
	value.Write(amount[:])
	writeVarBytes(&value, out.ScriptPubKey)
	return keyValue{key: []byte{psbtInWitnessUTXO}, value: value.Bytes()}
}

func nonWitnessUTXOField(tx *Tx) keyValue {
	return keyValue{key: []byte{psbtInNonWitnessUTXO}, value: tx.Bytes(false)}
}

func bip32InputField(path []uint32) keyValue {
	return bip32Field(psbtInBIP32Deriv, path)
}

func bip32OutputField(path []uint32) keyValue {
	return bip32Field(psbtOutBIP32Deriv, path)
}

func bip32Field(keyType byte, path []uint32) keyValue {
	pubkey := bytes.Repeat([]byte{0x02}, 33)
	key := append([]byte{keyType}, pubkey...)
	var value bytes.Buffer
	value.Write(testFingerprint[:])
	var child [4]byte
	for _, step := range path {
		binary.LittleEndian.PutUint32(child[:], step)
		value.Write(child[:])
	}
	return keyValue{key: key, value: value.Bytes()}
}

func sighashField(sighash uint32) keyValue {
	var value [4]byte
	binary.LittleEndian.PutUint32(value[:], sighash)
	return keyValue{key: []byte{psbtInSighashType}, value: value[:]}
}

func parseTestPSBT(t *testing.T, raw []byte) *PSBT {
	t.Helper()
	psbt, err := ParsePSBT(raw)
	if err != nil {
		t.Fatalf("ParsePSBT() error = %v", err)
	}
	return psbt
}

func assertPolicyCode(t *testing.T, err error, code string) {
	t.Helper()
	var policyErr *PolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("error %v is not *PolicyError", err)
	}
	if policyErr.Code != code {
		t.Fatalf("policy code = %q, want %q", policyErr.Code, code)
	}
}
