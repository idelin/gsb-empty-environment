package refresh

import (
	"encoding/json"
)

// AuditPair is the audit-safe view of one directed pair's verdict.
type AuditPair struct {
	Sender    string `json:"sender"`
	Recipient string `json:"recipient"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

// AuditMissing is the audit-safe view of a missing-material entry.
type AuditMissing struct {
	Sender    string `json:"sender"`
	Recipient string `json:"recipient"`
	What      string `json:"what"`
}

// AuditExcluded is the audit-safe view of a record kept out of this round.
type AuditExcluded struct {
	Sender    string `json:"sender,omitempty"`
	Recipient string `json:"recipient,omitempty"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason"`
	Round     uint64 `json:"round"`
}

// AuditReport is the only structure intended for logs and external auditors.
// By construction it carries participant IDs, statuses, reasons and digests —
// never custodian scalar values.
type AuditReport struct {
	Round          uint64          `json:"round"`
	Verdicts       []AuditPair     `json:"verdicts"`
	Missing        []AuditMissing  `json:"missing"`
	Excluded       []AuditExcluded `json:"excluded"`
	Disqualified   []string        `json:"disqualified"`
	PublishAllowed bool            `json:"publish_allowed"`
	GateReasons    []string        `json:"gate_reasons,omitempty"`
	Digest         string          `json:"transcript_digest,omitempty"`
}

// NewAuditReport projects internal evidence into the scalar-free audit shape.
func NewAuditReport(merged *MergedEvidence, triage *TriageReport, decision *PublishDecision) AuditReport {
	out := AuditReport{
		Round: triage.Round,
	}
	keys := make([]Pair, 0, len(triage.Verdicts))
	for k := range triage.Verdicts {
		keys = append(keys, k)
	}
	sortPairs(keys)
	for _, k := range keys {
		v := triage.Verdicts[k]
		out.Verdicts = append(out.Verdicts, AuditPair{
			Sender: k.Sender, Recipient: k.Recipient, Status: v.Status, Reason: v.Reason,
		})
	}
	for _, m := range triage.Missing {
		out.Missing = append(out.Missing, AuditMissing{
			Sender: m.Sender, Recipient: m.Recipient, What: m.What,
		})
	}
	for _, x := range merged.Excluded {
		out.Excluded = append(out.Excluded, AuditExcluded{
			Sender: x.Sender, Recipient: x.Recipient, Kind: x.Kind, Reason: x.Reason, Round: x.Round,
		})
	}
	out.Disqualified = sortedKeys(triage.Disqualified)
	if decision != nil {
		out.PublishAllowed = decision.Allowed
		out.GateReasons = append([]string(nil), decision.Reasons...)
		out.Digest = string(decision.Digest)
	}
	return out
}

// JSON renders the audit report. The method exists to make the serialization
// boundary explicit: audit callers never receive engine types directly.
func (a AuditReport) JSON() ([]byte, error) {
	return json.Marshal(a)
}

// PublicVerification is the all-in-one entry result for callers that hand over
// public evidence bundles and want a fully validated, auditable answer.
type PublicVerification struct {
	Merged   *MergedEvidence
	Triage   *TriageReport
	Decision *PublishDecision
	Audit    AuditReport
}

// PublicGateConfig supplies the expected topology for VerifyPublicEvidence.
type PublicGateConfig struct {
	Round            uint64
	Threshold        int
	OldMaster        []Point
	ExpectedConstant Point
	SenderIDs        []string
	RecipientIDs     []string
	Views            []RecipientView
}

// VerifyPublicEvidence merges public bundles, resolves aliases, triages every
// directed pair and evaluates the atomic publish gate. Missing material is
// reported down to (sender, recipient); a recipient's valid complaint can never
// be dropped just to make the sender sets agree.
func (e *Engine) VerifyPublicEvidence(cfg PublicGateConfig, bundles ...EvidenceBundle) (*PublicVerification, error) {
	merged, err := e.MergeEvidence(cfg.Round, bundles...)
	if err != nil {
		return nil, err
	}
	triage, err := e.Triage(cfg.Round, merged, cfg.SenderIDs, cfg.RecipientIDs)
	if err != nil {
		return nil, err
	}
	decision, err := e.PublishGate(cfg.Round, cfg.Threshold, cfg.OldMaster,
		cfg.ExpectedConstant, triage, cfg.Views)
	if err != nil {
		return nil, err
	}
	return &PublicVerification{
		Merged:   merged,
		Triage:   triage,
		Decision: decision,
		Audit:    NewAuditReport(merged, triage, decision),
	}, nil
}

func sortPairs(ps []Pair) {
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && pairLess(ps[j-1], ps[j]); j-- {
			ps[j-1], ps[j] = ps[j], ps[j-1]
		}
	}
}
