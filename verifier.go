package taskpkg

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Status is the terminal result of a trusted proof path.
type Status string

const (
	// StatusUnknown means no trusted terminal proof was available, or the
	// trusted evidence was inconsistent. It is the only safe answer for a
	// missing, expired, untrusted, or cyclic path.
	StatusUnknown Status = "unknown"
	// StatusAnswer is a positive terminal record (including a CNAME answer to
	// a CNAME query).
	StatusAnswer Status = "answer"
	// StatusNoData is a type-scoped negative at the terminal name.
	StatusNoData Status = "nodata"
	// StatusNXDomain is a name-scoped negative at the terminal name.
	StatusNXDomain Status = "nxdomain"
)

// IsNegative reports whether the status is a negative answer.
func (s Status) IsNegative() bool {
	return s == StatusNoData || s == StatusNXDomain
}

// IsDefinitive reports whether the status is a usable answer (positive or
// negative).
func (s Status) IsDefinitive() bool {
	return s == StatusAnswer || s.IsNegative()
}

// StepKind names one element of an authorization path.
type StepKind string

const (
	StepAlias  StepKind = "alias"
	StepAnswer StepKind = "answer"
	StepDenial StepKind = "denial"
)

// Step is one link in a complete authorization path. Zone is the authority
// that actually owns Name according to the finite zone tree. ValidFrom and
// ValidUntil are copied from the proof so the path is auditable.
type Step struct {
	Kind       StepKind
	Name       string
	Target     string
	Zone       string
	Type       RRType
	QType      RRType
	Value      string
	ProofID    string
	ValidFrom  time.Time
	ValidUntil time.Time
}

// Path is one independently enumerated trusted proof path. Paths that end in
// Unknown are retained so callers can see why no definitive path exists.
type Path struct {
	Query  Query
	Steps  []Step
	Status Status
	Name   string
	Type   RRType
	Value  string
	Reason string
	Cycle  bool
}

// Definitive reports whether this path produced a usable answer.
func (p Path) Definitive() bool {
	return p.Status.IsDefinitive()
}

func (p Path) identity() string {
	switch p.Status {
	case StatusAnswer:
		return "answer|" + p.Name + "|" + string(p.Type) + "|" + p.Value
	case StatusNoData:
		return "nodata|" + p.Name + "|" + string(p.Type)
	case StatusNXDomain:
		return "nxdomain|" + p.Name
	default:
		return "unknown|" + p.Reason
	}
}

// Ambiguity records inconsistent trusted evidence at one name. An ambiguity
// forces Resolve to return Unknown even if one branch happens to terminate
// definitively.
type Ambiguity struct {
	Name     string
	Reason   string
	ProofIDs []string
}

// Report is the result of enumerating all trusted paths from one query.
type Report struct {
	Query       Query
	Now         time.Time
	Paths       []Path
	Rejected    []Rejection
	Ambiguities []Ambiguity
}

// DefinitivePaths returns the usable answer paths, in deterministic order.
func (r Report) DefinitivePaths() []Path {
	var out []Path
	for _, p := range r.Paths {
		if p.Definitive() {
			out = append(out, p)
		}
	}
	return out
}

// NegativeScope is the precise cache scope of a negative answer. It is always
// the terminal name, never the starting name unless those names happen to be
// the same. Type is the original query type. NameWide is true only for
// NXDomain, where the terminal name itself does not exist.
type NegativeScope struct {
	Name     string
	Zone     string
	Type     RRType
	NameWide bool
}

// CacheKey is a conservative cache key for a definitive answer. For a
// negative answer it is always (terminal name, query type). In particular it
// is never keyed only by the starting name.
type CacheKey struct {
	Name string
	Type RRType
}

// Result is the selected result from a trusted-path analysis.
type Result struct {
	Query      Query
	Status     Status
	Name       string
	Zone       string
	Type       RRType
	Value      string
	Path       []Step
	Reason     string
	Cycle      bool
	Rejections []Rejection
}

// Definitive reports whether Result carries a usable answer.
func (r Result) Definitive() bool {
	return r.Status.IsDefinitive()
}

// AuthorizationPath returns the complete alias/terminal chain for a definitive
// result, or the partial chain that explains why an Unknown result stopped.
func (r Result) AuthorizationPath() []Step {
	out := make([]Step, len(r.Path))
	copy(out, r.Path)
	return out
}

// CompletePath reports whether the result has a definitive terminal proof and
// therefore a complete authorization path.
func (r Result) CompletePath() bool {
	return r.Definitive() && len(r.Path) > 0
}

// NegativeScope returns the exact name/type scope of a negative result.
func (r Result) NegativeScope() (NegativeScope, bool) {
	if !r.Status.IsNegative() {
		return NegativeScope{}, false
	}
	zone := r.Zone
	if zone == "" && len(r.Path) > 0 {
		zone = r.Path[len(r.Path)-1].Zone
	}
	return NegativeScope{
		Name:     r.Name,
		Zone:     zone,
		Type:     r.Type,
		NameWide: r.Status == StatusNXDomain,
	}, true
}

// CacheKey returns the conservative cache key for a definitive result.
func (r Result) CacheKey() (CacheKey, bool) {
	if !r.Definitive() {
		return CacheKey{}, false
	}
	return CacheKey{Name: r.Name, Type: r.Type}, true
}

// Verifier checks proof authority, freshness, type applicability, and path
// composition. Zones is the finite authoritative zone tree; Now is the
// resolution time. The zero value uses time.Now and default limits.
type Verifier struct {
	Zones    []string
	Now      time.Time
	MaxHops  int
	MaxPaths int
}

// Resolver is an alias for Verifier.
type Resolver = Verifier

// NewVerifier constructs a verifier for the given authoritative zones.
func NewVerifier(zones []string, now time.Time) *Verifier {
	return &Verifier{Zones: append([]string(nil), zones...), Now: now}
}

// NewResolver is an alias for NewVerifier.
func NewResolver(zones []string, now time.Time) *Resolver {
	return NewVerifier(zones, now)
}

// Resolve is a convenience wrapper for a one-shot verification.
func Resolve(zones []string, now time.Time, q Query, proofs []Proof) Result {
	return NewVerifier(zones, now).Resolve(q, proofs)
}

// Enumerate is a convenience wrapper for a one-shot path enumeration.
func Enumerate(zones []string, now time.Time, q Query, proofs []Proof) Report {
	return NewVerifier(zones, now).Enumerate(q, proofs)
}

func (v *Verifier) effective() Verifier {
	out := Verifier{}
	if v != nil {
		out = *v
	}
	if out.Now.IsZero() {
		out.Now = time.Now().UTC()
	}
	if out.MaxHops <= 0 {
		out.MaxHops = 64
	}
	if out.MaxPaths <= 0 {
		out.MaxPaths = 4096
	}
	return out
}

type indexedProof struct {
	proof      Proof
	name       string
	zone       string
	target     string
	typ        RRType
	qtype      RRType
	deniedType RRType
}

type index struct {
	aliases  map[string][]indexedProof
	denials  map[string][]indexedProof
	answers  map[string][]indexedProof
	rejected []Rejection
}

func proofSortKey(p indexedProof) string {
	return fmt.Sprintf("%d|%s|%s|%s|%s|%s|%s",
		p.proof.Kind, p.name, p.target, p.zone, p.typ, p.proof.QType, p.proof.ID)
}

// Enumerate returns every distinct trusted path from q, including paths that
// terminate in Unknown. It independently checks authority and freshness for
// each proof, so a forged or expired proof never becomes a path.
func (v *Verifier) Enumerate(q Query, proofs []Proof) Report {
	cfg := v.effective()
	start, qtype, err := q.canonical()
	rep := Report{
		Query: Query{Name: canonicalOrRaw(q.Name), Type: canonicalType(q.Type)},
		Now:   cfg.Now,
	}
	if rep.Query.Type == "" {
		rep.Query.Type = TypeAny
	}
	if err != nil {
		rep.Paths = append(rep.Paths, Path{
			Query:  rep.Query,
			Status: StatusUnknown,
			Name:   q.Name,
			Type:   qtype,
			Reason: "malformed query name: " + err.Error(),
		})
		return rep
	}
	rep.Query = Query{Name: start, Type: qtype}

	zs, err := NewZoneSet(cfg.Zones...)
	if err != nil {
		rep.Rejected = append(rep.Rejected, Rejection{
			Code:   RejectMalformed,
			Detail: "invalid zone tree: " + err.Error(),
		})
		rep.Paths = append(rep.Paths, Path{
			Query:  rep.Query,
			Status: StatusUnknown,
			Name:   start,
			Type:   qtype,
			Reason: "invalid zone tree",
		})
		return rep
	}

	idx := buildIndex(zs, qtype, cfg.Now, proofs)
	rep.Rejected = idx.rejected

	walk := &walker{
		cfg:      cfg,
		qtype:    qtype,
		index:    idx,
		report:   &rep,
		visitedA: make(map[string]struct{}),
		amb:      make(map[string]struct{}),
	}
	walk.visitedA[start] = struct{}{}
	walk.walk(start, []Step{})

	sortReport(&rep)
	return rep
}

// Resolve selects one definitive result from all trusted paths. Missing or
// inconsistent evidence yields Unknown.
func (v *Verifier) Resolve(q Query, proofs []Proof) Result {
	rep := v.Enumerate(q, proofs)
	return selectResult(rep)
}

// ResolveName is the name/type form of Resolve.
func (v *Verifier) ResolveName(name string, typ RRType, proofs []Proof) Result {
	return v.Resolve(Query{Name: name, Type: typ}, proofs)
}

// Verify is an alias for Resolve.
func (v *Verifier) Verify(q Query, proofs []Proof) Result {
	return v.Resolve(q, proofs)
}

// EnumerateName is the name/type form of Enumerate.
func (v *Verifier) EnumerateName(name string, typ RRType, proofs []Proof) Report {
	return v.Enumerate(Query{Name: name, Type: typ}, proofs)
}

func selectResult(rep Report) Result {
	res := Result{
		Query:      rep.Query,
		Status:     StatusUnknown,
		Name:       rep.Query.Name,
		Type:       rep.Query.Type,
		Rejections: rep.Rejected,
	}

	var definitive []Path
	for _, p := range rep.Paths {
		if p.Definitive() {
			definitive = append(definitive, p)
		}
	}

	if len(rep.Ambiguities) > 0 {
		res.Reason = "ambiguous trusted evidence: " + rep.Ambiguities[0].Reason
		return res
	}

	if len(definitive) == 0 {
		repPath := firstUnknown(rep.Paths)
		if repPath != nil {
			usePath(&res, *repPath)
			res.Status = StatusUnknown
			res.Name = repPath.Name
			res.Reason = repPath.Reason
			res.Cycle = repPath.Cycle
		} else {
			res.Reason = "no trusted proof path"
		}
		return res
	}

	ids := make(map[string]struct{})
	var chosen *Path
	for i := range definitive {
		id := definitive[i].identity()
		ids[id] = struct{}{}
		if chosen == nil || len(definitive[i].Steps) < len(chosen.Steps) {
			chosen = &definitive[i]
		}
	}
	if len(ids) != 1 {
		res.Status = StatusUnknown
		res.Name = rep.Query.Name
		res.Reason = "conflicting trusted proof paths"
		return res
	}
	usePath(&res, *chosen)
	return res
}

func usePath(res *Result, p Path) {
	res.Status = p.Status
	res.Name = p.Name
	res.Type = p.Type
	res.Value = p.Value
	res.Reason = p.Reason
	res.Cycle = p.Cycle
	res.Path = p.Steps
	res.Zone = ""
	if len(p.Steps) > 0 {
		res.Zone = p.Steps[len(p.Steps)-1].Zone
	}
}

func firstUnknown(paths []Path) *Path {
	for i := range paths {
		if !paths[i].Definitive() {
			return &paths[i]
		}
	}
	return nil
}

func canonicalOrRaw(name string) string {
	n, err := CanonicalName(name)
	if err != nil {
		return name
	}
	return n
}

func buildIndex(zs *ZoneSet, qtype RRType, now time.Time, proofs []Proof) index {
	idx := index{
		aliases: make(map[string][]indexedProof),
		denials: make(map[string][]indexedProof),
		answers: make(map[string][]indexedProof),
	}
	for _, p := range proofs {
		ip, rej := indexProof(zs, qtype, now, p)
		if rej != nil {
			idx.rejected = append(idx.rejected, *rej)
			continue
		}
		switch p.Kind {
		case ProofAlias:
			idx.aliases[ip.name] = append(idx.aliases[ip.name], ip)
		case ProofNXDomain, ProofNoData:
			idx.denials[ip.name] = append(idx.denials[ip.name], ip)
		case ProofAnswer:
			idx.answers[ip.name] = append(idx.answers[ip.name], ip)
		}
	}
	for name := range idx.aliases {
		sort.Slice(idx.aliases[name], func(i, j int) bool {
			return proofSortKey(idx.aliases[name][i]) < proofSortKey(idx.aliases[name][j])
		})
	}
	for name := range idx.denials {
		sort.Slice(idx.denials[name], func(i, j int) bool {
			return proofSortKey(idx.denials[name][i]) < proofSortKey(idx.denials[name][j])
		})
	}
	for name := range idx.answers {
		sort.Slice(idx.answers[name], func(i, j int) bool {
			return proofSortKey(idx.answers[name][i]) < proofSortKey(idx.answers[name][j])
		})
	}
	return idx
}

func indexProof(zs *ZoneSet, qtype RRType, now time.Time, p Proof) (indexedProof, *Rejection) {
	ip := indexedProof{proof: p}
	reject := func(code RejectionCode, detail string) (indexedProof, *Rejection) {
		return ip, &Rejection{
			ProofID: p.ID,
			Code:    code,
			Name:    p.Name,
			Zone:    p.Zone,
			Detail:  detail,
		}
	}

	if strings.TrimSpace(p.Name) == "" {
		return reject(RejectMalformed, "empty proof name")
	}
	name, err := CanonicalName(p.Name)
	if err != nil {
		return reject(RejectMalformed, err.Error())
	}
	ip.name = name

	if strings.TrimSpace(p.Zone) == "" {
		return reject(RejectMalformed, "empty proof zone")
	}
	zone, err := CanonicalName(p.Zone)
	if err != nil {
		return reject(RejectMalformed, "bad proof zone: "+err.Error())
	}
	ip.zone = zone

	authority, ok := zs.Authority(name)
	if !ok {
		return reject(RejectNoAuthority, "no configured authority for subject")
	}
	if zone != authority {
		return reject(RejectZoneMismatch,
			fmt.Sprintf("proof claims zone %q but %q is authoritative for %q", zone, authority, name))
	}

	if now.Before(p.ValidFrom) {
		return reject(RejectNotYetValid,
			fmt.Sprintf("proof is not valid before %s", p.ValidFrom.Format(time.RFC3339)))
	}
	if !p.ValidUntil.IsZero() && !now.Before(p.ValidUntil) {
		return reject(RejectExpired,
			fmt.Sprintf("proof expired at %s", p.ValidUntil.Format(time.RFC3339)))
	}

	ip.typ = canonicalType(p.Type)
	ip.qtype = canonicalType(p.QType)

	switch p.Kind {
	case ProofAlias:
		if strings.TrimSpace(p.Target) == "" {
			return reject(RejectInvalidProof, "alias has empty target")
		}
		target, err := CanonicalName(p.Target)
		if err != nil {
			return reject(RejectMalformed, "bad alias target: "+err.Error())
		}
		ip.target = target
		if !aliasTypeMatches(ip.typ, qtype) {
			return reject(RejectTypeMismatch,
				fmt.Sprintf("alias type %q does not apply to query type %q", ip.typ, qtype))
		}
		if !qTypeMatches(ip.qtype, qtype, true) {
			return reject(RejectTypeMismatch,
				fmt.Sprintf("alias is bound to query type %q, not %q", ip.qtype, qtype))
		}
	case ProofNXDomain:
		ip.deniedType = ip.typ
		if !ip.typ.IsAny() && ip.typ != qtype && !qtype.IsAny() {
			return reject(RejectTypeMismatch,
				fmt.Sprintf("nxdomain proof is bound to type %q, not %q", ip.typ, qtype))
		}
		if !qTypeMatches(ip.qtype, qtype, false) {
			return reject(RejectTypeMismatch,
				fmt.Sprintf("nxdomain proof is bound to query type %q, not %q", ip.qtype, qtype))
		}
	case ProofNoData:
		if ip.typ.IsAny() {
			if ip.qtype.IsAny() {
				return reject(RejectInvalidProof, "nodata proof names no concrete type or query type")
			}
			ip.deniedType = ip.qtype
		} else {
			ip.deniedType = ip.typ
		}
		if ip.deniedType != qtype && !qtype.IsAny() {
			return reject(RejectTypeMismatch,
				fmt.Sprintf("nodata proof denies type %q, not %q", ip.deniedType, qtype))
		}
		if !qTypeMatches(ip.qtype, qtype, false) {
			return reject(RejectTypeMismatch,
				fmt.Sprintf("nodata proof is bound to query type %q, not %q", ip.qtype, qtype))
		}
	case ProofAnswer:
		if ip.typ.IsAny() {
			return reject(RejectInvalidProof, "answer proof names no record type")
		}
		if !qtype.IsAny() && ip.typ != qtype {
			return reject(RejectTypeMismatch,
				fmt.Sprintf("answer type %q does not match query type %q", ip.typ, qtype))
		}
		if !qTypeMatches(ip.qtype, qtype, false) {
			return reject(RejectTypeMismatch,
				fmt.Sprintf("answer is bound to query type %q, not %q", ip.qtype, qtype))
		}
	default:
		return reject(RejectUnknownKind, fmt.Sprintf("unknown proof kind %d", p.Kind))
	}
	return ip, nil
}

func aliasTypeMatches(typ, qtype RRType) bool {
	if typ.IsAny() || typ == TypeCNAME {
		return true
	}
	return qtype.IsAny() || typ == qtype
}

func qTypeMatches(bound, qtype RRType, alias bool) bool {
	if bound.IsAny() {
		return true
	}
	if alias && bound == TypeCNAME {
		return true
	}
	return qtype.IsAny() || bound == qtype
}

type walker struct {
	cfg      Verifier
	qtype    RRType
	index    index
	report   *Report
	visitedA map[string]struct{}
	amb      map[string]struct{}
}

func (w *walker) walk(name string, steps []Step) {
	if len(w.report.Paths) >= w.cfg.MaxPaths {
		w.addUnknown(name, steps, "path enumeration limit reached", false)
		return
	}

	aliases := w.index.aliases[name]
	denials := w.index.denials[name]
	answers := w.index.answers[name]
	w.noteAmbiguities(name, aliases, denials, answers)

	created := false

	for _, d := range denials {
		step := stepFromProof(d)
		scopeType := w.qtype
		if scopeType.IsAny() && !d.deniedType.IsAny() {
			scopeType = d.deniedType
		}
		p := Path{
			Query:  w.report.Query,
			Steps:  appendStep(steps, step),
			Status: StatusNoData,
			Name:   name,
			Type:   scopeType,
			Reason: fmt.Sprintf("authenticated nodata at %s in zone %s", name, d.zone),
		}
		if d.proof.Kind == ProofNXDomain {
			p.Status = StatusNXDomain
			p.Reason = fmt.Sprintf("authenticated nxdomain at %s in zone %s", name, d.zone)
		}
		w.report.Paths = append(w.report.Paths, p)
		created = true
	}

	for _, a := range answers {
		step := stepFromProof(a)
		w.report.Paths = append(w.report.Paths, Path{
			Query:  w.report.Query,
			Steps:  appendStep(steps, step),
			Status: StatusAnswer,
			Name:   name,
			Type:   a.typ,
			Value:  a.proof.Value,
			Reason: fmt.Sprintf("authenticated answer at %s in zone %s", name, a.zone),
		})
		created = true
	}

	if w.qtype == TypeCNAME {
		// For a CNAME query the alias record itself is the terminal answer.
		for _, a := range aliases {
			step := stepFromProof(a)
			step.Type = TypeCNAME
			w.report.Paths = append(w.report.Paths, Path{
				Query:  w.report.Query,
				Steps:  appendStep(steps, step),
				Status: StatusAnswer,
				Name:   name,
				Type:   TypeCNAME,
				Value:  a.target,
				Reason: fmt.Sprintf("authenticated cname answer at %s in zone %s", name, a.zone),
			})
			created = true
		}
	} else {
		for _, a := range aliases {
			step := stepFromProof(a)
			next := a.target
			if _, seen := w.visitedA[next]; seen {
				w.report.Paths = append(w.report.Paths, Path{
					Query:  w.report.Query,
					Steps:  appendStep(steps, step),
					Status: StatusUnknown,
					Name:   next,
					Type:   w.qtype,
					Reason: fmt.Sprintf("alias cycle: %s -> %s", name, next),
					Cycle:  true,
				})
				created = true
				continue
			}
			if len(steps)+1 >= w.cfg.MaxHops {
				w.addUnknown(next, appendStep(steps, step), "alias chain exceeds maximum hop count", false)
				created = true
				continue
			}
			nextVisited := make(map[string]struct{}, len(w.visitedA)+1)
			for k := range w.visitedA {
				nextVisited[k] = struct{}{}
			}
			nextVisited[next] = struct{}{}
			old := w.visitedA
			w.visitedA = nextVisited
			w.walk(next, appendStep(steps, step))
			w.visitedA = old
			created = true
		}
	}

	if !created {
		hint := ""
		if reason := w.rejectedHint(name); reason != "" {
			hint = ": " + reason
		}
		w.addUnknown(name, steps, "missing trusted alias or denial proof for "+name+hint, false)
	}
}

func (w *walker) addUnknown(name string, steps []Step, reason string, cycle bool) {
	w.report.Paths = append(w.report.Paths, Path{
		Query:  w.report.Query,
		Steps:  append([]Step(nil), steps...),
		Status: StatusUnknown,
		Name:   name,
		Type:   w.qtype,
		Reason: reason,
		Cycle:  cycle,
	})
}

func (w *walker) rejectedHint(name string) string {
	var expired, zone, other bool
	for _, r := range w.report.Rejected {
		n, err := CanonicalName(r.Name)
		if err != nil || n != name {
			continue
		}
		switch r.Code {
		case RejectExpired:
			expired = true
		case RejectZoneMismatch, RejectNoAuthority:
			zone = true
		default:
			other = true
		}
	}
	switch {
	case expired:
		return "a proof for this name is expired"
	case zone:
		return "a proof for this name is not signed by its authority"
	case other:
		return "no applicable proof for this query type"
	default:
		return ""
	}
}

func (w *walker) noteAmbiguities(name string, aliases, denials, answers []indexedProof) {
	add := func(reason string, proofs ...indexedProof) {
		key := name + "|" + reason
		if _, ok := w.amb[key]; ok {
			return
		}
		w.amb[key] = struct{}{}
		var ids []string
		for _, p := range proofs {
			if p.proof.ID != "" {
				ids = append(ids, p.proof.ID)
			}
		}
		w.report.Ambiguities = append(w.report.Ambiguities, Ambiguity{
			Name:     name,
			Reason:   reason,
			ProofIDs: ids,
		})
	}

	targets := make(map[string]struct{})
	for _, a := range aliases {
		targets[a.target] = struct{}{}
	}
	if len(targets) > 1 {
		add(fmt.Sprintf("multiple trusted aliases for %s", name), aliases...)
	}

	if len(aliases) > 0 && (len(denials) > 0 || len(answers) > 0) {
		add(fmt.Sprintf("alias coexists with terminal proof for %s", name),
			concatProofs(aliases, denials, answers)...)
	}

	negKinds := make(map[string]struct{})
	for _, d := range denials {
		if d.proof.Kind == ProofNXDomain {
			negKinds["nxdomain"] = struct{}{}
		} else {
			negKinds["nodata:"+string(d.deniedType)] = struct{}{}
		}
	}
	if len(negKinds) > 1 {
		add(fmt.Sprintf("multiple conflicting negative proofs for %s", name),
			concatProofs(denials, answers)...)
	}
	if len(denials) > 0 && len(answers) > 0 {
		add(fmt.Sprintf("negative and positive proofs coexist for %s", name),
			concatProofs(denials, answers)...)
	}

	values := make(map[string]struct{})
	for _, a := range answers {
		values[string(a.typ)+"|"+a.proof.Value] = struct{}{}
	}
	if len(values) > 1 {
		add(fmt.Sprintf("multiple positive answers for %s", name), answers...)
	}
}

func concatProofs(groups ...[]indexedProof) []indexedProof {
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	out := make([]indexedProof, 0, total)
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func stepFromProof(ip indexedProof) Step {
	kind := StepDenial
	switch ip.proof.Kind {
	case ProofAlias:
		kind = StepAlias
	case ProofAnswer:
		kind = StepAnswer
	}
	typ := ip.typ
	if kind == StepAlias && typ.IsAny() {
		typ = TypeCNAME
	}
	return Step{
		Kind:       kind,
		Name:       ip.name,
		Target:     ip.target,
		Zone:       ip.zone,
		Type:       typ,
		QType:      ip.qtype,
		Value:      ip.proof.Value,
		ProofID:    ip.proof.ID,
		ValidFrom:  ip.proof.ValidFrom,
		ValidUntil: ip.proof.ValidUntil,
	}
}

func appendStep(steps []Step, step Step) []Step {
	out := make([]Step, len(steps), len(steps)+1)
	copy(out, steps)
	return append(out, step)
}

func sortReport(rep *Report) {
	sort.Slice(rep.Rejected, func(i, j int) bool {
		ri, rj := rep.Rejected[i], rep.Rejected[j]
		if ri.Name != rj.Name {
			return ri.Name < rj.Name
		}
		if ri.Code != rj.Code {
			return ri.Code < rj.Code
		}
		return ri.ProofID < rj.ProofID
	})
	sort.Slice(rep.Ambiguities, func(i, j int) bool {
		if rep.Ambiguities[i].Name != rep.Ambiguities[j].Name {
			return rep.Ambiguities[i].Name < rep.Ambiguities[j].Name
		}
		return rep.Ambiguities[i].Reason < rep.Ambiguities[j].Reason
	})
	sort.Slice(rep.Paths, func(i, j int) bool {
		si, sj := pathSortKey(rep.Paths[i]), pathSortKey(rep.Paths[j])
		if si != sj {
			return si < sj
		}
		return rep.Paths[i].Reason < rep.Paths[j].Reason
	})
}

func pathSortKey(p Path) string {
	var b strings.Builder
	b.WriteString(string(p.Status))
	b.WriteByte('|')
	b.WriteString(p.Name)
	b.WriteByte('|')
	b.WriteString(string(p.Type))
	b.WriteByte('|')
	b.WriteString(p.Value)
	for _, s := range p.Steps {
		b.WriteByte('|')
		b.WriteString(string(s.Kind))
		b.WriteByte(':')
		b.WriteString(s.Name)
		b.WriteByte('>')
		b.WriteString(s.Target)
		b.WriteByte('@')
		b.WriteString(s.Zone)
		b.WriteByte('#')
		b.WriteString(s.ProofID)
	}
	return b.String()
}
