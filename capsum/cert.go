package capsum

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"prescreen.local/ps-2237843e01/pathlang"
)

// Certificate is a self-contained, version-bound analysis artifact. An
// independent finite checker (package certcheck) can verify the initial
// conditions, call/return pairing and every asynchronous boundary without
// trusting the producer's result claims.
type Certificate struct {
	SchemaVersion string `json:"schema_version"`

	ProgramBinding ProgramBinding `json:"program_binding"`
	Roots          []Root         `json:"roots"`
	Domains        []Domain       `json:"domains"`

	Alphabet          []string `json:"alphabet"`
	ComponentAlphabet []string `json:"component_alphabet"`

	Generators []CertGenerator `json:"generators"`
	Handles    []CertHandle    `json:"handles"`
	Points     []CertPoint     `json:"points"`
	Edges      []Edge          `json:"edges"`

	MaxHandles      int  `json:"max_handles"`
	Budget          int  `json:"budget"`
	BudgetExhausted bool `json:"budget_exhausted"`
	Iterations      int  `json:"iterations"`

	// Evidence records the abstract configurations that reached each point and
	// the labels that grounded each call/consume entry, for boundary checks.
	Evidence CertEvidence `json:"evidence"`

	// InputHash binds the exact program, domain and root-version payload.
	InputHash string `json:"input_hash"`
}

// ProgramBinding names and versions the analyzed program together with the
// tenant-root view it was analyzed against.
type ProgramBinding struct {
	Name          string `json:"name"`
	EntryNode     string `json:"entry_node"`
	RootViewHash  string `json:"root_view_hash"`
	DomainVersion string `json:"domain_version"`
}

// CertGenerator serializes one canonical grant-atom DFA.
type CertGenerator struct {
	ID    int           `json:"id"`
	Grant Grant         `json:"grant"`
	DFA   *pathlang.DFA `json:"dfa"`
}

// CertHandle is the frozen-language summary for one queue slot.
type CertHandle struct {
	Name    string        `json:"name"`
	Bit     uint16        `json:"bit"`
	Present bool          `json:"present"`
	May     *pathlang.DFA `json:"may,omitempty"`
	Must    *pathlang.DFA `json:"must,omitempty"`
}

// CertPoint is the per-program-point may/must verdict with DFA witnesses.
type CertPoint struct {
	Node      string        `json:"node"`
	Status    Status        `json:"status"`
	May       *pathlang.DFA `json:"may,omitempty"`
	Must      *pathlang.DFA `json:"must,omitempty"`
	ReachedBy []uint16      `json:"reached_by"`
}

// CertEvidence exposes fixpoint bookkeeping needed by the finite checker.
type CertEvidence struct {
	Labels         []CertLabel      `json:"labels"`
	EdgeLabels     map[string][]int `json:"edge_labels"`
	ParkedReturns  []CertParked     `json:"parked_returns"`
	HandleFreezeOK []string         `json:"handle_freeze_edges"`
}

// CertLabel records one method-entry abstract context.
type CertLabel struct {
	ID     int               `json:"id"`
	EntryS []json.RawMessage `json:"entry_s"`
	EntryF []json.RawMessage `json:"entry_f"`
}

// CertParked records a caller frame parked on a call/consume edge.
type CertParked struct {
	EdgeID    string `json:"edge_id"`
	FromLabel int    `json:"from_label"`
	Mask      uint16 `json:"mask"`
}

// Certificate builds a serializable, hash-bound certificate from a result. The
// analysis is re-run internally only to collect evidence; the public Result is
// the source of all reported languages.
func (r *Result) Certificate(p *Program, a *analysis) *Certificate {
	cert := &Certificate{
		SchemaVersion: "capsum/v1",
		ProgramBinding: ProgramBinding{
			Name:          p.Name,
			EntryNode:     p.EntryNode,
			DomainVersion: hashDomains(p.Domains),
		},
		Roots:             append([]Root(nil), p.Roots...),
		Domains:           append([]Domain(nil), p.Domains...),
		Alphabet:          append([]string(nil), r.Alphabet...),
		ComponentAlphabet: append([]string(nil), r.Components...),
		Edges:             append([]Edge(nil), p.Edges...),
		MaxHandles:        p.MaxHandles,
		Budget:            r.Budget,
		BudgetExhausted:   r.BudgetExhausted,
		Iterations:        r.Iterations,
		Evidence: CertEvidence{
			EdgeLabels:     map[string][]int{},
			HandleFreezeOK: nil,
		},
	}
	cert.ProgramBinding.RootViewHash = hashRoots(p.Roots)

	for _, rec := range r.GeneratorRecords {
		cert.Generators = append(cert.Generators, CertGenerator{
			ID: rec.ID, Grant: rec.Grant, DFA: rec.DFA,
		})
	}

	handleNames := make([]string, 0, len(r.Handles))
	for name := range r.Handles {
		handleNames = append(handleNames, name)
	}
	sort.Strings(handleNames)
	for _, name := range handleNames {
		hs := r.Handles[name]
		bit := uint16(1) << uint(a.x.handleIndex[name])
		cert.Handles = append(cert.Handles, CertHandle{
			Name: name, Bit: bit, Present: hs.Present, May: hs.May, Must: hs.Must,
		})
	}

	for _, node := range p.Nodes {
		pr := r.Points[node]
		cp := CertPoint{
			Node: node, Status: pr.Status, May: pr.May, Must: pr.Must,
			ReachedBy: append([]uint16(nil), pr.ReachedBy...),
		}
		cert.Points = append(cert.Points, cp)
	}

	for id, lab := range a.labels {
		cert.Evidence.Labels = append(cert.Evidence.Labels, CertLabel{
			ID:     id,
			EntryS: dnfToJSON(lab.entryS),
			EntryF: dnfToJSON(lab.entryF),
		})
	}
	for ei, labels := range a.edgeLabels {
		cert.Evidence.EdgeLabels[p.Edges[ei].ID] = append([]int(nil), labels...)
	}
	for ei, parked := range a.parked {
		for _, rt := range parked {
			cert.Evidence.ParkedReturns = append(cert.Evidence.ParkedReturns, CertParked{
				EdgeID: p.Edges[ei].ID, FromLabel: rt.fromLabel, Mask: rt.mask,
			})
		}
	}
	for ei := range p.Edges {
		if p.Edges[ei].Kind == EdgePost {
			cert.Evidence.HandleFreezeOK = append(cert.Evidence.HandleFreezeOK, p.Edges[ei].ID)
		}
	}
	sort.Slice(cert.Evidence.ParkedReturns, func(i, j int) bool {
		return cert.Evidence.ParkedReturns[i].EdgeID < cert.Evidence.ParkedReturns[j].EdgeID
	})

	cert.InputHash = cert.computeInputHash(p)
	return cert
}

func dnfToJSON(d dnf) []json.RawMessage {
	if d == nil {
		return nil
	}
	out := make([]json.RawMessage, 0, len(d))
	for _, t := range d {
		raw, _ := json.Marshal([]int(t))
		out = append(out, raw)
	}
	return out
}

func (c *Certificate) computeInputHash(p *Program) string {
	payload := struct {
		Program *Program `json:"program"`
		Roots   []Root   `json:"roots"`
		Domains []Domain `json:"domains"`
	}{Program: p, Roots: p.Roots, Domains: p.Domains}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func hashRoots(roots []Root) string {
	raw, _ := json.Marshal(roots)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func hashDomains(domains []Domain) string {
	raw, _ := json.Marshal(domains)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Marshal serializes the certificate to canonical JSON.
func (c *Certificate) Marshal() ([]byte, error) {
	return json.MarshalIndent(c, "", "  ")
}
