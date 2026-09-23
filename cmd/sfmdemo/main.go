// Command sfmdemo demonstrates the frequency models, MDL structure
// selection and the byte-level codec end to end.
package main

import (
	"fmt"

	"sfm/codec"
	"sfm/freq"
)

func main() {
	data := []byte("the quick brown fox jumps over the lazy dog. the dog barks, the fox runs.")
	fmt.Printf("input: %q (%d bytes)\n\n", data, len(data))

	// --- Order-0 model: incremental stats, entropy estimate ----------
	m := freq.NewModel(256)
	for _, s := range data {
		m.Add(s)
	}
	fmt.Printf("[order-0] total=%d distinct=%d entropy=%.4f bits/sym (estimate)\n",
		m.Total(), len(m.Symbols()), m.Entropy())
	fmt.Printf("[order-0] smoothed code length estimate: %.1f bits\n", m.CodeLength())

	// Incremental retract: remove the last sentence, totals stay
	// conserved (adds - retracts).
	last := []byte(" the fox runs.")
	for _, s := range last {
		if err := m.Retract(s); err != nil {
			panic(err)
		}
	}
	fmt.Printf("[order-0] after retracting %d samples: total=%d\n", len(last), m.Total())
	if err := m.Retract(0x00); err != nil {
		fmt.Printf("[order-0] retract of absent symbol rejected: %v\n", err)
	}

	// --- Conditional model: contexts, merge, no pollution ------------
	cm := freq.NewCondModel(256)
	prev := byte(codec.InitContext)
	for _, s := range data {
		cm.Add(prev, s)
		prev = s
	}
	fmt.Printf("\n[order-1] contexts=%d total=%d entropy=%.4f bits/sym (estimate)\n",
		len(cm.Contexts()), cm.Total(), cm.Entropy())
	cm.MergeContexts('x', 'g')
	shared := cm.Count('g', ' ')
	if err := cm.Retract('x', ' '); err != nil {
		panic(err)
	}
	fmt.Printf("[order-1] merged 'x'+'g'; retract via 'x': ctx 'g' count of ' ' stays %d (no pollution)\n",
		shared)
	if cm.Count('g', ' ') != shared {
		panic("shared counts polluted")
	}

	// --- MDL structure selection on a small domain -------------------
	// 4 contexts, 2 symbols; context 0,1 favour 0, context 2,3 favour 1.
	hist := [][]uint64{{90, 10}, {85, 15}, {12, 88}, {9, 91}}
	assign, modelBits, dataBits := freq.SelectExhaustive(hist, 2, freq.DefaultK)
	fmt.Printf("\n[mdl] best structure assign=%v model=%.1f bits data=%.1f bits total=%.1f bits\n",
		assign, modelBits, dataBits, modelBits+dataBits)

	// --- Real byte encoding ------------------------------------------
	full := freq.NewModel(256)
	for _, s := range data {
		full.Add(s)
	}
	blob, err := codec.EncodeOrder0(full, data)
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n[codec] order-0 stream: %d bytes (header+checksum+payload)\n", len(blob))
	fmt.Printf("[codec] entropy estimate: %.1f bytes -> real length is the encoded %d bytes\n",
		full.Entropy()*float64(len(data))/8, len(blob))

	blob1, err := codec.EncodeConditional(cm, data)
	if err != nil {
		panic(err)
	}
	fmt.Printf("[codec] order-1 stream: %d bytes (30 contexts x 256 code-length bytes of model header:\n", len(blob1))
	fmt.Printf("[codec]   the header cost dominates tiny inputs, exactly what MDL penalizes)\n")

	out, err := codec.Decode(blob, 1<<20)
	if err != nil {
		panic(err)
	}
	fmt.Printf("[codec] decode round trip ok: %v\n", string(out) == string(data))
	if _, err := codec.Decode(blob, 10); err != nil {
		fmt.Printf("[codec] decode with limit 10 rejected: %v\n", err)
	}

	// Deterministic input: entropy is exactly 0, real length is not.
	det := freq.NewModel(256)
	detData := make([]byte, 64)
	for i := range detData {
		detData[i] = 'a'
		det.Add('a')
	}
	detBlob, _ := codec.EncodeOrder0(det, detData)
	fmt.Printf("\n[deterministic] entropy=%v bits/sym, real stream=%d bytes (entropy is only an estimate)\n",
		det.Entropy(), len(detBlob))
}
