package netsec

import (
	"fmt"
	"hash"
)

type sliceSlot struct {
	b     []byte
	isNil bool
}

type parsedExpandLabel struct {
	hash      func() hash.Hash
	secret    []byte
	secretSet bool
	label     string
	labelSet  bool
	context   []byte
	length    int
	lengthSet bool
}

func parseExpandLabelArgs(args []any) (parsedExpandLabel, error) {
	p := parsedExpandLabel{hash: defaultHash()}
	var slots []sliceSlot
	for _, arg := range args {
		if h, ok := hashFromValue(arg); ok {
			p.hash = h
			continue
		}
		switch v := arg.(type) {
		case string:
			if !p.labelSet {
				p.label = v
				p.labelSet = true
			}
		case []byte:
			slots = append(slots, sliceSlot{b: v})
		case nil:
			slots = append(slots, sliceSlot{isNil: true})
		default:
			if n, ok := intFromValue(v); ok {
				p.length = n
				p.lengthSet = true
			}
		}
	}
	if len(slots) > 0 {
		p.secret = slots[0].b
		p.secretSet = !slots[0].isNil
	}
	if len(slots) > 1 {
		p.context = slots[1].b
	}
	if !p.secretSet {
		return p, ErrInvalidSecret
	}
	if !p.labelSet {
		return p, ErrNoLabel
	}
	if !p.lengthSet {
		return p, ErrNoLength
	}
	return p, nil
}

type parsedDeriveSecret struct {
	hash           func() hash.Hash
	secret         []byte
	secretSet      bool
	label          string
	labelSet       bool
	transcript     []byte
	transcriptHash hash.Hash
}

func parseDeriveSecretArgs(args []any) (parsedDeriveSecret, error) {
	p := parsedDeriveSecret{hash: defaultHash()}
	var slots []sliceSlot
	for _, arg := range args {
		if h, ok := hashFromValue(arg); ok {
			p.hash = h
			continue
		}
		switch v := arg.(type) {
		case string:
			if !p.labelSet {
				p.label = v
				p.labelSet = true
			}
		case []byte:
			slots = append(slots, sliceSlot{b: v})
		case nil:
			slots = append(slots, sliceSlot{isNil: true})
		case hash.Hash:
			p.transcriptHash = v
		default:
			_ = v
		}
	}
	if len(slots) > 0 {
		p.secret = slots[0].b
		p.secretSet = !slots[0].isNil
	}
	if len(slots) > 1 {
		p.transcript = slots[1].b
	}
	if !p.secretSet {
		return p, ErrInvalidSecret
	}
	if !p.labelSet {
		return p, ErrNoLabel
	}
	return p, nil
}

type parsedPhaseDerivation struct {
	phase          Phase
	hash           func() hash.Hash
	secret         []byte
	secretSet      bool
	typedSecret    bool
	transcript     []byte
	transcriptHash hash.Hash
}

func parsePhaseDerivationArgs(args []any, required Phase) (parsedPhaseDerivation, error) {
	p := parsedPhaseDerivation{phase: required, hash: defaultHash()}
	var slots []sliceSlot
	var phaseSet bool
	for _, arg := range args {
		if h, ok := hashFromValue(arg); ok {
			p.hash = h
			continue
		}
		switch v := arg.(type) {
		case Phase:
			if !v.Valid() {
				return p, ErrInvalidPhase
			}
			p.phase = v
			phaseSet = true
		case string:
			if ph, err := ParsePhase(v); err == nil {
				p.phase = ph
				phaseSet = true
			}
		case []byte:
			slots = append(slots, sliceSlot{b: v})
		case nil:
			slots = append(slots, sliceSlot{isNil: true})
		case hash.Hash:
			p.transcriptHash = v
		case *EarlySecret:
			if v == nil {
				return p, ErrInvalidSecret
			}
			if required != PhaseEarly {
				return p, fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, PhaseEarly, required)
			}
			p.secret = cloneBytes(v.secret)
			p.secretSet = true
			p.typedSecret = true
			p.hash = chooseHash(v.hash)
		case *MasterSecret:
			if v == nil {
				return p, ErrInvalidSecret
			}
			if required != PhasePostHandshake {
				return p, fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, PhasePostHandshake, required)
			}
			p.secret = cloneBytes(v.secret)
			p.secretSet = true
			p.typedSecret = true
			p.hash = chooseHash(v.hash)
		}
	}
	if phaseSet && p.phase != required {
		return p, fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, p.phase, required)
	}
	if len(slots) > 0 {
		if p.typedSecret {
			p.transcript = slots[0].b
		} else {
			p.secret = slots[0].b
			p.secretSet = !slots[0].isNil
		}
	}
	if len(slots) > 1 {
		if !p.typedSecret {
			p.transcript = slots[1].b
		}
	}
	if !p.secretSet {
		return p, ErrInvalidSecret
	}
	return p, nil
}

type parsedExport struct {
	phase     Phase
	phaseSet  bool
	hash      func() hash.Hash
	label     string
	labelSet  bool
	context   []byte
	length    int
	lengthSet bool
}

func parseExportArgs(args []any) (parsedExport, error) {
	p := parsedExport{hash: defaultHash()}
	var slots []sliceSlot
	for _, arg := range args {
		if h, ok := hashFromValue(arg); ok {
			p.hash = h
			continue
		}
		switch v := arg.(type) {
		case Phase:
			if !v.Valid() {
				return p, ErrInvalidPhase
			}
			p.phase = v
			p.phaseSet = true
		case string:
			if !p.labelSet {
				p.label = v
				p.labelSet = true
			}
		case []byte:
			slots = append(slots, sliceSlot{b: v})
		case nil:
			slots = append(slots, sliceSlot{isNil: true})
		default:
			if n, ok := intFromValue(v); ok {
				p.length = n
				p.lengthSet = true
			}
		}
	}
	if len(slots) > 0 {
		p.context = slots[0].b
	}
	if !p.labelSet {
		return p, ErrNoLabel
	}
	if !p.lengthSet {
		return p, ErrNoLength
	}
	return p, nil
}

type parsedVerify struct {
	phase      Phase
	phaseSet   bool
	hash       func() hash.Hash
	label      string
	labelSet   bool
	context    []byte
	expected   []byte
	haveExpect bool
	length     int
	lengthSet  bool
}

func parseVerifyArgs(args []any) (parsedVerify, error) {
	p := parsedVerify{hash: defaultHash()}
	var slots []sliceSlot
	for _, arg := range args {
		if h, ok := hashFromValue(arg); ok {
			p.hash = h
			continue
		}
		switch v := arg.(type) {
		case Phase:
			if !v.Valid() {
				return p, ErrInvalidPhase
			}
			p.phase = v
			p.phaseSet = true
		case string:
			if !p.labelSet {
				p.label = v
				p.labelSet = true
			}
		case []byte:
			slots = append(slots, sliceSlot{b: v})
		case nil:
			slots = append(slots, sliceSlot{isNil: true})
		default:
			if n, ok := intFromValue(v); ok {
				p.length = n
				p.lengthSet = true
			}
		}
	}

	var nonNil []int
	for i, s := range slots {
		if !s.isNil {
			nonNil = append(nonNil, i)
		}
	}
	switch {
	case len(nonNil) == 0:
		return p, ErrInvalidSecret
	case len(nonNil) == 1:
		p.expected = slots[nonNil[0]].b
		p.haveExpect = true
	case len(nonNil) >= 2:
		p.context = slots[nonNil[0]].b
		p.expected = slots[nonNil[len(nonNil)-1]].b
		p.haveExpect = true
	}
	if !p.haveExpect {
		return p, ErrVerificationFailed
	}
	if !p.lengthSet {
		p.length = len(p.expected)
	}
	if !p.labelSet {
		return p, ErrNoLabel
	}
	return p, nil
}

type parsedTopExport struct {
	phase          Phase
	phaseSet       bool
	hash           func() hash.Hash
	exporter       *Exporter
	earlySecret    *EarlyExporterMasterSecret
	postSecret     *ExporterMasterSecret
	earlyBase      *EarlySecret
	masterBase     *MasterSecret
	rawSecret      []byte
	haveRawSecret  bool
	transcriptHash hash.Hash
	slots          []sliceSlot
	label          string
	labelSet       bool
	length         int
	lengthSet      bool
}

func parseTopExportArgs(args []any) (parsedTopExport, error) {
	p := parsedTopExport{hash: defaultHash()}
	for _, arg := range args {
		if h, ok := hashFromValue(arg); ok {
			p.hash = h
			continue
		}
		switch v := arg.(type) {
		case *Exporter:
			p.exporter = v
		case *EarlyExporterMasterSecret:
			p.earlySecret = v
			if !p.phaseSet && v != nil {
				p.phase = PhaseEarly
				p.phaseSet = true
			}
		case *ExporterMasterSecret:
			p.postSecret = v
			if !p.phaseSet && v != nil {
				p.phase = PhasePostHandshake
				p.phaseSet = true
			}
		case *EarlySecret:
			p.earlyBase = v
			if !p.phaseSet && v != nil {
				p.phase = PhaseEarly
				p.phaseSet = true
			}
		case *MasterSecret:
			p.masterBase = v
			if !p.phaseSet && v != nil {
				p.phase = PhasePostHandshake
				p.phaseSet = true
			}
		case *ExportResult:
			if v != nil {
				p.label = v.Label
				p.labelSet = true
				p.slots = append(p.slots, sliceSlot{b: v.Context})
			}
		case Phase:
			if !v.Valid() {
				return p, ErrInvalidPhase
			}
			p.phase = v
			p.phaseSet = true
		case string:
			if ph, err := ParsePhase(v); err == nil && !p.phaseSet && !p.labelSet {
				p.phase = ph
				p.phaseSet = true
				continue
			}
			if !p.labelSet {
				p.label = v
				p.labelSet = true
			}
		case []byte:
			p.slots = append(p.slots, sliceSlot{b: v})
		case nil:
			p.slots = append(p.slots, sliceSlot{isNil: true})
		case hash.Hash:
			p.transcriptHash = v
		default:
			if n, ok := intFromValue(v); ok {
				p.length = n
				p.lengthSet = true
			}
		}
	}
	if !p.labelSet {
		return p, ErrNoLabel
	}
	if !p.lengthSet {
		return p, ErrNoLength
	}
	if p.exporter != nil {
		return p, nil
	}
	if p.earlySecret != nil || p.postSecret != nil {
		return p, nil
	}
	if p.earlyBase != nil || p.masterBase != nil {
		return p, nil
	}
	if len(p.slots) == 0 {
		return p, ErrInvalidSecret
	}
	p.rawSecret = p.slots[0].b
	p.haveRawSecret = !p.slots[0].isNil
	if !p.haveRawSecret {
		return p, ErrInvalidSecret
	}
	if !p.phaseSet {
		return p, ErrInvalidPhase
	}
	return p, nil
}

func topExportContext(p parsedTopExport) []byte {
	switch {
	case len(p.slots) >= 3:
		return p.slots[2].b
	case len(p.slots) == 2:
		return p.slots[1].b
	default:
		return nil
	}
}

func topExportDerivesFromBase(p parsedTopExport) bool {
	if p.transcriptHash != nil {
		return true
	}
	if len(p.slots) >= 3 {
		return true
	}
	return false
}

func topExportTranscript(p parsedTopExport) ([]byte, hash.Hash) {
	if p.transcriptHash != nil {
		return nil, p.transcriptHash
	}
	if len(p.slots) >= 3 {
		return p.slots[1].b, nil
	}
	return nil, nil
}

type parsedTopVerify struct {
	phase          Phase
	phaseSet       bool
	hash           func() hash.Hash
	exporter       *Exporter
	earlySecret    *EarlyExporterMasterSecret
	postSecret     *ExporterMasterSecret
	earlyBase      *EarlySecret
	masterBase     *MasterSecret
	transcriptHash hash.Hash
	transcript     []byte
	rawSecret      []byte
	rawDerives     bool
	haveSecret     bool
	label          string
	labelSet       bool
	context        []byte
	expected       []byte
	haveExpect     bool
	length         int
	lengthSet      bool
}

func (p parsedTopVerify) earlySecretOrPost() any {
	if p.earlySecret != nil {
		return p.earlySecret
	}
	return p.postSecret
}

func parseTopVerifyArgs(args []any) (parsedTopVerify, error) {
	p := parsedTopVerify{hash: defaultHash()}
	var slots []sliceSlot
	for _, arg := range args {
		if h, ok := hashFromValue(arg); ok {
			p.hash = h
			continue
		}
		switch v := arg.(type) {
		case *Exporter:
			p.exporter = v
		case *EarlyExporterMasterSecret:
			p.earlySecret = v
			if !p.phaseSet && v != nil {
				p.phase = PhaseEarly
				p.phaseSet = true
			}
		case *ExporterMasterSecret:
			p.postSecret = v
			if !p.phaseSet && v != nil {
				p.phase = PhasePostHandshake
				p.phaseSet = true
			}
		case *EarlySecret:
			p.earlyBase = v
			if !p.phaseSet && v != nil {
				p.phase = PhaseEarly
				p.phaseSet = true
			}
		case *MasterSecret:
			p.masterBase = v
			if !p.phaseSet && v != nil {
				p.phase = PhasePostHandshake
				p.phaseSet = true
			}
		case Phase:
			if !v.Valid() {
				return p, ErrInvalidPhase
			}
			p.phase = v
			p.phaseSet = true
		case string:
			if ph, err := ParsePhase(v); err == nil && !p.phaseSet && !p.labelSet {
				p.phase = ph
				p.phaseSet = true
				continue
			}
			if !p.labelSet {
				p.label = v
				p.labelSet = true
			}
		case []byte:
			slots = append(slots, sliceSlot{b: v})
		case nil:
			slots = append(slots, sliceSlot{isNil: true})
		case hash.Hash:
			p.transcriptHash = v
		default:
			if n, ok := intFromValue(v); ok {
				p.length = n
				p.lengthSet = true
			}
		}
	}

	if p.exporter == nil && p.earlySecret == nil && p.postSecret == nil &&
		p.earlyBase == nil && p.masterBase == nil {
		var nonNil []int
		for i, s := range slots {
			if !s.isNil {
				nonNil = append(nonNil, i)
			}
		}
		if len(nonNil) == 0 {
			return p, ErrInvalidSecret
		}
		p.rawSecret = slots[nonNil[0]].b
		p.haveSecret = true
		switch {
		case p.transcriptHash != nil:
			p.rawDerives = true
			p.expected = slots[nonNil[len(nonNil)-1]].b
			p.haveExpect = true
			if len(slots) >= 3 {
				p.context = slots[1].b
			}
		case len(slots) >= 4:
			p.rawDerives = true
			p.transcript = slots[1].b
			p.context = slots[2].b
			p.expected = slots[nonNil[len(nonNil)-1]].b
			p.haveExpect = true
		case len(slots) == 3:
			p.context = slots[1].b
			p.expected = slots[2].b
			p.haveExpect = true
		case len(slots) == 2:
			p.expected = slots[1].b
			p.haveExpect = true
		}
	} else if p.earlyBase != nil || p.masterBase != nil {
		var nonNil []int
		for i, s := range slots {
			if !s.isNil {
				nonNil = append(nonNil, i)
			}
		}
		if len(nonNil) == 0 {
			return p, ErrVerificationFailed
		}
		p.expected = slots[nonNil[len(nonNil)-1]].b
		p.haveExpect = true
		if p.transcriptHash == nil && len(slots) >= 3 {
			p.transcript = slots[0].b
			p.context = slots[1].b
		} else if len(nonNil) >= 2 {
			p.context = slots[nonNil[0]].b
		}
	} else {
		var nonNil []int
		for i, s := range slots {
			if !s.isNil {
				nonNil = append(nonNil, i)
			}
		}
		if len(nonNil) >= 1 {
			p.expected = slots[nonNil[len(nonNil)-1]].b
			p.haveExpect = true
		}
		if len(nonNil) >= 2 {
			p.context = slots[nonNil[0]].b
		}
	}
	if !p.haveExpect {
		return p, ErrVerificationFailed
	}
	if !p.lengthSet {
		p.length = len(p.expected)
	}
	if !p.labelSet {
		return p, ErrNoLabel
	}
	if p.exporter == nil && p.earlySecret == nil && p.postSecret == nil &&
		p.earlyBase == nil && p.masterBase == nil && !p.phaseSet {
		return p, ErrInvalidPhase
	}
	return p, nil
}

func parsePhaseSecretsVerifyArgs(args []any) (phase Phase, early, post []byte, label string, context, expected []byte, h func() hash.Hash, err error) {
	h = defaultHash()
	var phaseSet, labelSet bool
	var slots []sliceSlot
	for _, arg := range args {
		if hf, ok := hashFromValue(arg); ok {
			h = hf
			continue
		}
		switch v := arg.(type) {
		case Phase:
			if !v.Valid() {
				err = ErrInvalidPhase
				return
			}
			phase, phaseSet = v, true
		case string:
			if ph, perr := ParsePhase(v); perr == nil && !phaseSet && !labelSet {
				phase, phaseSet = ph, true
				continue
			}
			if !labelSet {
				label, labelSet = v, true
			}
		case []byte:
			slots = append(slots, sliceSlot{b: v})
		case nil:
			slots = append(slots, sliceSlot{isNil: true})
		}
	}
	if !phaseSet || !phase.Valid() {
		err = ErrInvalidPhase
		return
	}
	if !labelSet {
		err = ErrNoLabel
		return
	}
	if len(slots) < 3 {
		err = ErrInvalidSecret
		return
	}
	early, post = slots[0].b, slots[1].b
	last := len(slots) - 1
	for last > 1 && slots[last].isNil {
		last--
	}
	if last < 2 {
		err = ErrVerificationFailed
		return
	}
	expected = slots[last].b
	if len(slots) >= 4 {
		context = slots[2].b
	}
	return
}
