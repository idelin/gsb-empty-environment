package artifactflow

// The types and helpers in this file exist solely to let the external
// brute-force oracle test inspect the indexed representation. They are
// compiled only during testing and are not part of the importable API.

type ExportSlotUse struct {
	Receipt  int
	Consumed bool
}

type ExportRealization struct {
	StepID    string
	Candidate string
	Inputs    []ExportSlotUse
	Outputs   []int
}

type ExportReceipt struct {
	Receipt Receipt
	Path    Path
}

type ExportIndexed struct {
	Receipts []ExportReceipt
	solved   *solved
}

func (e *ExportIndexed) ReceiptsFor(path, digest string) []int {
	nd := normalizeDigest(Digest(digest))
	var out []int
	for i, ir := range e.solved.receipts {
		if string(ir.path) == path && ir.normDigest == nd {
			out = append(out, i)
		}
	}
	return out
}

type ExportEnum struct {
	All     []ExportRealization
	ByStep  [][]int
	Indexed *ExportIndexed
}

func ExportPrepare(p *Problem) (*solved, error) {
	s, err := prepare(p)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func ExportEnumerate(s *solved) (*ExportEnum, error) {
	en, err := enumerate(s)
	if err != nil {
		return nil, err
	}
	out := &ExportEnum{Indexed: &ExportIndexed{solved: s}}
	for _, ir := range s.receipts {
		out.Indexed.Receipts = append(out.Indexed.Receipts, ExportReceipt{
			Receipt: ir.receipt, Path: ir.path,
		})
	}
	for _, r := range en.all {
		er := ExportRealization{StepID: r.stepID, Candidate: r.candidate}
		for _, ci := range s.steps {
			for _, cand := range ci.candidates {
				if cand.stepID != r.stepID || cand.candidate.Name != r.candidate {
					continue
				}
				for _, si := range cand.inputs {
					er.Inputs = append(er.Inputs, ExportSlotUse{
						Receipt:  r.inputs[si.slot.Name],
						Consumed: si.slot.Consumed,
					})
				}
			}
		}
		er.Outputs = append(er.Outputs, r.outputsList()...)
		out.All = append(out.All, er)
	}
	out.ByStep = en.byStep
	return out, nil
}

func (r realization) outputsList() []int {
	out := make([]int, 0, len(r.outputs))
	for _, v := range r.outputs {
		out = append(out, v)
	}
	return out
}
