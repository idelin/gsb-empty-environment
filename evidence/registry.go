package evidence

import (
	"sort"
)

// BuilderRecord 是构建器在注册表中的登记项。
type BuilderRecord struct {
	ID      BuilderID `json:"id"`
	Domain  DomainID  `json:"domain"`
	PubKey  []byte    `json:"pubkey"`
	Revoked bool      `json:"revoked"`
}

// DomainRecord 是信任域的当前登记项。Name 可重复（重建同名域），
// DomainID 不可复用，因此“同名不同身份”天然可区分。
type DomainRecord struct {
	ID       DomainID    `json:"id"`
	Name     string      `json:"name"`
	Builders []BuilderID `json:"builders"` // 排序
}

// 治理事件类型。
const (
	GovAddDomain      = "add_domain"
	GovRenameDomain   = "rename_domain"
	GovMergeDomains   = "merge_domains"
	GovSplitDomain    = "split_domain"
	GovAddBuilder     = "add_builder"
	GovRevokeBuilders = "revoke_builders"
)

// RegistryEvent 是一条信任域治理变更。顺序应用即重建注册表。
type RegistryEvent struct {
	Kind string `json:"kind"`

	// 域登记/改名
	DomainID DomainID `json:"domain_id,omitempty"`
	Name     string   `json:"name,omitempty"`

	// 合并：Source 域并入 Target，Source 注销，构建器迁移到 Target
	Source DomainID `json:"source,omitempty"`
	Target DomainID `json:"target,omitempty"`

	// 分裂：From 保留在 KeepBuilders 的构建器，其余迁入 NewDomain
	From         DomainID    `json:"from,omitempty"`
	NewDomainID  DomainID    `json:"new_domain_id,omitempty"`
	NewName      string      `json:"new_name,omitempty"`
	MoveBuilders []BuilderID `json:"move_builders,omitempty"`

	// 构建器登记/撤销
	Builder  BuilderID   `json:"builder,omitempty"`
	PubKey   []byte      `json:"pubkey,omitempty"`
	Builders []BuilderID `json:"builders,omitempty"` // 批量撤销
	Reason   string      `json:"reason,omitempty"`
}

// Revocation 是撤销登记表中的一条事实，供独立重验逐条核对。
type Revocation struct {
	Domain  DomainID  `json:"domain"`
	Builder BuilderID `json:"builder"`
	Reason  string    `json:"reason"`
	Seq     uint64    `json:"seq"`
}

// RegistryState 是某一序列号上的完整注册表视图与撤销日志。
type RegistryState struct {
	Seq         uint64                      `json:"seq"`
	Domains     map[DomainID]DomainRecord   `json:"-"`
	Builders    map[BuilderID]BuilderRecord `json:"-"`
	Revocations []Revocation                `json:"revocations"`
}

// GenesisRegistry 构造空注册表。
func GenesisRegistry() *RegistryState {
	return &RegistryState{
		Domains:  map[DomainID]DomainRecord{},
		Builders: map[BuilderID]BuilderRecord{},
	}
}

// Clone 返回深拷贝，事件应用不修改旧视图（支持差分重放旧状态）。
func (r *RegistryState) Clone() *RegistryState {
	cp := &RegistryState{Seq: r.Seq, Revocations: append([]Revocation(nil), r.Revocations...)}
	cp.Domains = make(map[DomainID]DomainRecord, len(r.Domains))
	for k, v := range r.Domains {
		v.Builders = append([]BuilderID(nil), v.Builders...)
		cp.Domains[k] = v
	}
	cp.Builders = make(map[BuilderID]BuilderRecord, len(r.Builders))
	for k, v := range r.Builders {
		v.PubKey = append([]byte(nil), v.PubKey...)
		cp.Builders[k] = v
	}
	return cp
}

// Apply 在严格校验下应用一条治理事件，返回新一代注册表。
func (r *RegistryState) Apply(ev RegistryEvent) (*RegistryState, error) {
	next := r.Clone()
	next.Seq++
	switch ev.Kind {
	case GovAddDomain:
		if ev.DomainID == "" {
			return nil, ErrUnknownDomain
		}
		if _, ok := next.Domains[ev.DomainID]; ok {
			return nil, ErrDomainExists
		}
		next.Domains[ev.DomainID] = DomainRecord{ID: ev.DomainID, Name: ev.Name}
	case GovRenameDomain:
		d, ok := next.Domains[ev.DomainID]
		if !ok {
			return nil, ErrUnknownDomain
		}
		d.Name = ev.Name
		next.Domains[ev.DomainID] = d
	case GovAddBuilder:
		d, ok := next.Domains[ev.DomainID]
		if !ok {
			return nil, ErrUnknownDomain
		}
		if _, dup := next.Builders[ev.Builder]; dup {
			return nil, ErrDomainExists
		}
		next.Builders[ev.Builder] = BuilderRecord{
			ID: ev.Builder, Domain: ev.DomainID, PubKey: append([]byte(nil), ev.PubKey...),
		}
		d.Builders = append(d.Builders, ev.Builder)
		sort.Slice(d.Builders, func(i, j int) bool { return d.Builders[i] < d.Builders[j] })
		next.Domains[ev.DomainID] = d
	case GovRevokeBuilders:
		for _, b := range ev.Builders {
			rec, ok := next.Builders[b]
			if !ok {
				return nil, ErrUnknownDomain
			}
			rec.Revoked = true
			next.Builders[b] = rec
			next.Revocations = append(next.Revocations, Revocation{
				Domain: rec.Domain, Builder: b, Reason: ev.Reason, Seq: next.Seq,
			})
		}
	case GovMergeDomains:
		src, ok1 := next.Domains[ev.Source]
		tgt, ok2 := next.Domains[ev.Target]
		if !ok1 || !ok2 || ev.Source == ev.Target {
			return nil, ErrUnknownDomain
		}
		for _, b := range src.Builders {
			rec := next.Builders[b]
			rec.Domain = ev.Target
			next.Builders[b] = rec
			tgt.Builders = append(tgt.Builders, b)
		}
		sort.Slice(tgt.Builders, func(i, j int) bool { return tgt.Builders[i] < tgt.Builders[j] })
		next.Domains[ev.Target] = tgt
		delete(next.Domains, ev.Source)
	case GovSplitDomain:
		from, ok := next.Domains[ev.From]
		if !ok {
			return nil, ErrUnknownDomain
		}
		if _, dup := next.Domains[ev.NewDomainID]; dup {
			return nil, ErrDomainExists
		}
		move := map[BuilderID]bool{}
		for _, b := range ev.MoveBuilders {
			move[b] = true
		}
		var keep, moved []BuilderID
		for _, b := range from.Builders {
			if move[b] {
				moved = append(moved, b)
			} else {
				keep = append(keep, b)
			}
		}
		if len(moved) != len(ev.MoveBuilders) {
			return nil, ErrUnknownDomain
		}
		for _, b := range moved {
			rec := next.Builders[b]
			rec.Domain = ev.NewDomainID
			next.Builders[b] = rec
		}
		from.Builders = keep
		next.Domains[ev.From] = from
		sort.Slice(moved, func(i, j int) bool { return moved[i] < moved[j] })
		next.Domains[ev.NewDomainID] = DomainRecord{
			ID: ev.NewDomainID, Name: ev.NewName, Builders: moved,
		}
	default:
		return nil, ErrUnknownDomain
	}
	return next, nil
}

// registryWire 是注册表参与哈希与序列化的确定形式（map 展平为有序切片）。
type registryWire struct {
	Seq         uint64          `json:"seq"`
	Domains     []DomainRecord  `json:"domains"`
	Builders    []BuilderRecord `json:"builders"`
	Revocations []Revocation    `json:"revocations"`
}

func (r *RegistryState) toWire() registryWire {
	w := registryWire{Seq: r.Seq, Revocations: append([]Revocation(nil), r.Revocations...)}
	for _, d := range r.Domains {
		d.Builders = append([]BuilderID(nil), d.Builders...)
		w.Domains = append(w.Domains, d)
	}
	sort.Slice(w.Domains, func(i, j int) bool { return w.Domains[i].ID < w.Domains[j].ID })
	for _, b := range r.Builders {
		w.Builders = append(w.Builders, b)
	}
	sort.Slice(w.Builders, func(i, j int) bool { return w.Builders[i].ID < w.Builders[j].ID })
	return w
}

// RegistryFromWire 从确定形式重建可查询注册表。
func RegistryFromWire(w registryWire) *RegistryState {
	r := GenesisRegistry()
	r.Seq = w.Seq
	r.Revocations = append([]Revocation(nil), w.Revocations...)
	for _, d := range w.Domains {
		r.Domains[d.ID] = d
	}
	for _, b := range w.Builders {
		r.Builders[b.ID] = b
	}
	return r
}

// Hash 计算注册表状态摘要。
func (r *RegistryState) Hash() (Digest, error) {
	raw, err := canonicalJSON(struct {
		Tag string       `json:"tag"`
		Reg registryWire `json:"reg"`
	}{Tag: tagRegistry, Reg: r.toWire()})
	if err != nil {
		return Digest{}, err
	}
	return sha256Sum(raw), nil
}

// IsWitnessValid 依据当前注册表判定一条见证是否有效：
// 域仍存在、构建器仍登记在该域且未被撤销、公钥与登记一致。
func (r *RegistryState) IsWitnessValid(w Witness) bool {
	rec, ok := r.Builders[w.Builder]
	if !ok || rec.Revoked || rec.Domain != w.Domain {
		return false
	}
	if _, ok := r.Domains[w.Domain]; !ok {
		return false
	}
	return bytesEqual(rec.PubKey, w.PubKey)
}

// RevocationReason 返回某构建器登记的撤销原因，未撤销返回空串与 false。
func (r *RegistryState) RevocationReason(b BuilderID) (string, bool) {
	for i := len(r.Revocations) - 1; i >= 0; i-- {
		if r.Revocations[i].Builder == b {
			return r.Revocations[i].Reason, true
		}
	}
	return "", false
}
