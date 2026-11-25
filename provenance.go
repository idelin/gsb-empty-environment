package artifactflow

import "sort"

type provenance struct {
	roots    map[string]struct{}
	receipts map[string]struct{}
	signers  map[Role]struct{}
}

func newProvenance() provenance {
	return provenance{
		roots:    make(map[string]struct{}),
		receipts: make(map[string]struct{}),
		signers:  make(map[Role]struct{}),
	}
}

func (p *provenance) union(other provenance) {
	for v := range other.roots {
		p.roots[v] = struct{}{}
	}
	for v := range other.receipts {
		p.receipts[v] = struct{}{}
	}
	for v := range other.signers {
		p.signers[v] = struct{}{}
	}
}

func (p *provenance) addReceipt(id string, signer Role) {
	p.receipts[id] = struct{}{}
	p.signers[signer] = struct{}{}
}

func (p provenance) rootList() []string {
	return sortedStringSet(p.roots)
}

func (p provenance) receiptList() []string {
	return sortedStringSet(p.receipts)
}

func (p provenance) signerList() []Role {
	set := make(map[string]struct{}, len(p.signers))
	for role := range p.signers {
		set[role] = struct{}{}
	}
	return sortedStringSet(set)
}

// outputProvenance computes the roots carried by one output of a receipt.
// When a rename rule (or exact identity) binds the output to one or more
// inputs, only those inputs contribute. Otherwise the output conservatively
// carries the union of all inputs. The receipt's own signer is always added.
func outputProvenance(rc *receiptInfo, inputs []provenance, outputIndex int) provenance {
	p := newProvenance()
	if len(inputs) == 0 {
		p.roots[sourceReceiptRootPrefix+rc.key] = struct{}{}
		p.addReceipt(rc.key, rc.receipt.Signer)
		return p
	}
	boundByOutput := make([][]int, len(rc.outputs))
	inputBound := make([]bool, len(inputs))
	for outIdx := range rc.outputs {
		for inIdx := range inputs {
			if !inputMatchesOutput(rc, inIdx, outIdx) {
				continue
			}
			boundByOutput[outIdx] = append(boundByOutput[outIdx], inIdx)
			inputBound[inIdx] = true
		}
	}
	bound := boundByOutput[outputIndex]
	if len(bound) == 0 {
		for i := range inputs {
			p.union(inputs[i])
		}
	} else {
		for _, i := range bound {
			p.union(inputs[i])
		}
		// Inputs not explicitly bound to any output are still part of the
		// step and must not silently lose their signed provenance.
		for i := range inputs {
			if !inputBound[i] {
				p.union(inputs[i])
			}
		}
	}
	p.addReceipt(rc.key, rc.receipt.Signer)
	return p
}

func inputMatchesOutput(rc *receiptInfo, inputIndex, outputIndex int) bool {
	in := rc.inputs[inputIndex]
	out := rc.outputs[outputIndex]
	if in.Path == out.Path {
		return true
	}
	for i := range rc.rules {
		if rc.rules[i].permits(in.Path, out.Path) {
			return true
		}
	}
	return false
}

func sortedStringSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
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
