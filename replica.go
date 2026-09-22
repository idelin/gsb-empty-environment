package causalslice

import "sort"

// Replica is one multi-object causally consistent store. It keeps every
// received version (even when dependencies are not here yet) and tracks the
// convergent latest version per key.
type Replica struct {
	sim         *Sim
	id          string
	seq         int
	versions    map[VersionID]*Version
	keyOf       map[VersionID]string
	latest      map[string]*Version
	crashed     bool
	gossipEvery int
}

type gossipTag struct{}

// NewReplica registers a replica and starts its periodic anti-entropy timer.
func NewReplica(sim *Sim, id string, gossipEvery int) *Replica {
	r := &Replica{
		sim:         sim,
		id:          id,
		versions:    map[VersionID]*Version{},
		keyOf:       map[VersionID]string{},
		latest:      map[string]*Version{},
		gossipEvery: gossipEvery,
	}
	sim.Register(r)
	sim.Schedule(id, sim.Time()+gossipEvery, gossipTag{})
	return r
}

func (r *Replica) ID() string           { return r.id }
func (r *Replica) down() bool           { return r.crashed }
func (r *Replica) has(v VersionID) bool { return r.versions[v] != nil }

// Crash stops processing; in-flight packets to this replica are discarded.
func (r *Replica) Crash() {
	if r.crashed {
		return
	}
	r.crashed = true
	r.sim.Log("%s crashes", r.id)
}

// Recover re-enters the group; durable storage survives, anti-entropy and
// retried fetches repair any writes missed while down.
func (r *Replica) Recover() {
	if !r.crashed {
		return
	}
	r.crashed = false
	r.sim.Schedule(r.id, r.sim.Time()+r.gossipEvery, gossipTag{})
	r.sim.Log("%s recovers", r.id)
}

// PeerIDs is the sorted roster, used by clients and gossip.
func (s *Sim) PeerIDs() []string {
	ids := make([]string, 0, len(s.actors))
	for id, a := range s.actors {
		if _, ok := a.(*Replica); ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (r *Replica) Receive(sim *Sim, env Envelope) {
	switch m := env.Msg.(type) {
	case PutReq:
		r.handlePut(env.From, m)
	case FetchReq:
		r.handleFetch(env.From, m)
	case Replicate:
		r.merge(m.Ver, "replicate")
	case GossipReq:
		all := make([]*Version, 0, len(r.versions))
		for _, v := range r.versions {
			all = append(all, v)
		}
		sort.Slice(all, func(i, j int) bool { return lessVID(all[i].ID, all[j].ID) })
		sim.Send(Envelope{From: r.id, To: env.From, Msg: GossipResp{Vers: all}})
	case GossipResp:
		ids := make([]VersionID, 0, len(m.Vers))
		for _, v := range m.Vers {
			r.merge(v, "gossip")
			ids = append(ids, v.ID)
		}
		if len(ids) > 0 {
			sim.Log("%s merged gossip from %s: %s", r.id, env.From, joinVIDs(ids))
		}
	case Timer:
		if _, ok := m.Tag.(gossipTag); ok {
			for _, p := range sim.PeerIDs() {
				if p != r.id {
					sim.Send(Envelope{From: r.id, To: p, Msg: GossipReq{}})
				}
			}
			sim.Schedule(r.id, sim.Time()+r.gossipEvery, gossipTag{})
		}
	}
}

// ErrCauseMissing is returned when a write names a dependency the replica has
// not seen: no version is created, forcing the client to obtain the cause
// before the effect can be produced.
var ErrCauseMissing = causeMissingErr{}

type causeMissingErr struct{}

func (causeMissingErr) Error() string { return "causal dependency missing at target replica" }

func (r *Replica) handlePut(client string, req PutReq) {
	missing := r.missingClosure(req.KnownDeps)
	if len(missing) > 0 {
		r.sim.Log("%s rejects put(%s)=%s: missing causes %s", r.id, req.Key, req.Val, joinVIDs(missing))
		r.sim.Send(Envelope{From: r.id, To: client, Msg: PutAck{Key: req.Key, Err: ErrCauseMissing, Tag: req.Tag}})
		return
	}
	r.seq++
	vc := mergeVC(nil, nil)
	for _, d := range req.KnownDeps {
		if v := r.versions[d]; v != nil {
			vc = mergeVC(vc, v.VC)
		}
	}
	vc[r.id] = vc[r.id] + 1
	id := VersionID{Origin: r.id, Seq: r.seq}
	ver := &Version{ID: id, Key: req.Key, Val: req.Val, Deps: dedupVID(req.KnownDeps), VC: vc}
	r.merge(ver, "local write")
	r.sim.Log("%s writes %s=%s %s", r.id, req.Key, req.Val, id)
	for _, p := range r.sim.PeerIDs() {
		if p != r.id {
			r.sim.Send(Envelope{From: r.id, To: p, Msg: Replicate{Ver: ver}})
		}
	}
	r.sim.Send(Envelope{From: r.id, To: client, Msg: PutAck{Key: req.Key, Ver: ver, Tag: req.Tag}})
}

func (r *Replica) handleFetch(client string, req FetchReq) {
	var root *Version
	if req.Dep != (VersionID{}) {
		root = r.versions[req.Dep]
	} else {
		root = r.latest[req.Key]
	}
	closure, missing := r.closureFrom(root)
	r.sim.Send(Envelope{From: r.id, To: client, Msg: FetchResp{
		Key: req.Key, Ver: root, Closure: closure, Missing: missing, Token: req.Token,
	}})
}

// closureFrom returns every locally known ancestor of root and the frontier
// of ancestor ids that are not stored here.
func (r *Replica) closureFrom(root *Version) ([]*Version, []VersionID) {
	if root == nil {
		return nil, nil
	}
	seen := map[VersionID]bool{}
	queue := []*Version{root}
	known := []*Version{}
	var missing []VersionID
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if v == nil || seen[v.ID] {
			continue
		}
		seen[v.ID] = true
		known = append(known, v)
		for _, d := range v.Deps {
			if dv := r.versions[d]; dv != nil {
				if !seen[d] {
					queue = append(queue, dv)
				}
			} else if !seen[d] {
				seen[d] = true
				missing = append(missing, d)
			}
		}
	}
	sort.Slice(known, func(i, j int) bool { return lessVID(known[i].ID, known[j].ID) })
	sort.Slice(missing, func(i, j int) bool { return lessVID(missing[i], missing[j]) })
	return known, missing
}

// missingClosure returns ids in the transitive closure of deps absent here.
func (r *Replica) missingClosure(deps []VersionID) []VersionID {
	seen := map[VersionID]bool{}
	var missing []VersionID
	queue := append([]VersionID(nil), deps...)
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		if seen[d] {
			continue
		}
		seen[d] = true
		if v := r.versions[d]; v != nil {
			queue = append(queue, v.Deps...)
		} else {
			missing = append(missing, d)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return lessVID(missing[i], missing[j]) })
	return missing
}

func (r *Replica) merge(v *Version, why string) {
	if v == nil || r.has(v.ID) {
		return
	}
	r.versions[v.ID] = v
	r.keyOf[v.ID] = v.Key
	old := r.latest[v.Key]
	if old == nil || dominates(v.VC, old.VC) ||
		(!dominates(old.VC, v.VC) && v.ID.Origin < old.ID.Origin) {
		r.latest[v.Key] = v
	}
	if why != "" {
		r.sim.Log("%s stores %s (%s=%s)", r.id, v.ID, v.Key, v.Val)
	}
}

// mergeVC returns the componentwise maximum of a and b (new map).
func mergeVC(a, b map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if v > out[k] {
			out[k] = v
		}
	}
	return out
}

// dominates reports whether a is strictly after b in the vector-clock order.
func dominates(a, b map[string]int) bool {
	strict := false
	for k, v := range a {
		if w := b[k]; v > w {
			strict = true
		} else if v < w {
			return false
		}
	}
	for k, v := range b {
		if a[k] < v {
			return false
		}
	}
	return strict
}

func dedupVID(ids []VersionID) []VersionID {
	seen := map[VersionID]bool{}
	out := ids[:0:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return lessVID(out[i], out[j]) })
	return out
}

func lessVID(a, b VersionID) bool {
	if a.Origin != b.Origin {
		return a.Origin < b.Origin
	}
	return a.Seq < b.Seq
}

func joinVIDs(ids []VersionID) string {
	if len(ids) == 0 {
		return "-"
	}
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id.String()
	}
	return out
}
