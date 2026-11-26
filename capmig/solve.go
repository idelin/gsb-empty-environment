package capmig

import (
	"sort"
	"time"
)

// Solve performs the full-history enumeration and returns an auditable
// migration decision set plus the minimum re-authentication subjects.
func Solve(in *Input) (*Result, error) {
	start := time.Now()

	idx, err := buildIndex(in)
	if err != nil {
		return nil, err
	}
	e := newEngine(in, idx)
	e.enumerate()

	admissibleCount := e.admissibleCount()
	res := &Result{
		BarrierEpoch:        in.BarrierEpoch,
		TotalHistories:      e.numHist,
		AdmissibleHistories: admissibleCount,
		Inconsistent:        admissibleCount == 0,
		ExclusivityHeld:     e.exclusivityHeld,
	}

	res.AliasTable = buildAliasTable(in, idx)
	res.RejectedReceipts = buildRejectedReceipts(in, e)
	res.AmbiguousVariables = buildVariableInfos(in, idx, e)

	coverCache := map[string][]uint64{}
	coverKey := func(token, subject string, req requirement) string {
		return token + "|" + subject + "|" + req.physical + "|" + req.action
	}

	setsToCover := [][]uint64{}
	candidateSubjects := []string{}
	candidateSeen := map[string]int{}
	addCandidate := func(subject string) int {
		if i, ok := candidateSeen[subject]; ok {
			return i
		}
		i := len(candidateSubjects)
		candidateSeen[subject] = i
		candidateSubjects = append(candidateSubjects, subject)
		return i
	}

	for _, t := range in.Tickets {
		report := e.evaluateTicket(t, coverCache, coverKey)
		res.TicketReports = append(res.TicketReports, report)

		if report.Decision != DecisionMigrate {
			subjects := append([]string(nil), report.ReauthCandidates...)
			if len(subjects) == 0 {
				subjects = []string{t.Subject}
			}
			cover := make([]uint64, len(e.admissible))
			for _, s := range subjects {
				i := addCandidate(s)
				cover[i/64] |= 1 << uint(i%64)
			}
			setsToCover = append(setsToCover, cover)
		}
	}

	chosen := minimumHittingSet(candidateSubjects, setsToCover)
	res.MinimumReauthSubjects = chosen

	// Joint consistency is structural: one global mask drives every ticket.
	res.JointConsistencyHeld = verifyJointConsistency(in, e)
	res.SolveMillis = time.Since(start).Milliseconds()
	return res, nil
}

type requirement struct {
	physical string
	action   string
}

func buildAliasTable(in *Input, idx *index) []AliasResolution {
	out := make([]AliasResolution, 0, len(in.AliasBindings))
	for _, b := range in.AliasBindings {
		out = append(out, AliasResolution{Alias: b.Alias, PhysicalID: idx.resolveObject(b.Alias)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
}

func buildRejectedReceipts(in *Input, e *engine) []RejectedReceipt {
	var out []RejectedReceipt
	for hi, h := range in.Handoffs {
		if !e.rejected[hi] {
			continue
		}
		receiptEpoch := h.ReceiptEpoch
		if receiptEpoch == 0 {
			receiptEpoch = h.Epoch
		}
		out = append(out, RejectedReceipt{
			HandoffID:    handoffID(in, hi),
			Token:        h.Token,
			ReceiptEpoch: receiptEpoch,
			BarrierEpoch: in.BarrierEpoch,
		})
	}
	return out
}

func handoffID(in *Input, hi int) string {
	if in.Handoffs[hi].ID != "" {
		return in.Handoffs[hi].ID
	}
	return "hop-" + itoa(hi)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func buildVariableInfos(in *Input, idx *index, e *engine) []VariableInfo {
	out := make([]VariableInfo, 0, len(idx.ambiguousHops))
	for varIdx, hi := range idx.ambiguousHops {
		h := in.Handoffs[hi]
		out = append(out, VariableInfo{
			Index:     varIdx,
			HandoffID: handoffID(in, hi),
			Token:     h.Token,
			From:      h.From,
			To:        h.To,
			Effective: e.variableEffective(varIdx, hi),
		})
	}
	return out
}
