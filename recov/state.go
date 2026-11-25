package recov

import (
	"bytes"
)

// instanceState 是把某个目录前缀折叠后，一台设备实例的授权视图。
type instanceState struct {
	account  AccountID
	device   DeviceName
	instance InstanceID
	active   bool
	idKey    []byte
	opkRoot  []byte
	opkCount uint32
	revoked  map[uint32]bool
}

// branchState 是一条可成立目录分支在某个检查点处的完整视图。
type branchState struct {
	size       uint64
	root       []byte
	events     []*Event
	instances  map[InstanceID]*instanceState
	nameToInst map[DeviceName]InstanceID
}

func newBranchState() *branchState {
	return &branchState{
		instances:  map[InstanceID]*instanceState{},
		nameToInst: map[DeviceName]InstanceID{},
	}
}

// foldEvents 把有序事件前缀折叠为分支视图，并校验事件序号连续、语义合法。
func foldEvents(events []*Event) (*branchState, error) {
	st := newBranchState()
	st.size = uint64(len(events))
	for i, ev := range events {
		if ev.Seq != uint64(i) {
			return nil, wrapErr(ErrMalformed, "event seq %d != position %d", ev.Seq, i)
		}
		if err := st.apply(ev); err != nil {
			return nil, err
		}
		st.events = append(st.events, ev)
	}
	return st, nil
}

func (s *branchState) apply(ev *Event) error {
	switch ev.Kind {
	case EvRegister:
		// 同名设备在仍有活跃实例时不得注册；重注册前必须先注销，
		// 且新实例拥有全新实例标识与全新密钥，绝不继承旧实例。
		if cur, ok := s.nameToInst[ev.Device]; ok {
			if s.instances[cur].active {
				return wrapErr(ErrReRegistration,
					"device %q re-registered as %s while %s still active", ev.Device, ev.Instance, cur)
			}
		}
		if _, exists := s.instances[ev.Instance]; exists {
			return wrapErr(ErrReRegistration, "instance id %s reused", ev.Instance)
		}
		if len(ev.IdentityKey) == 0 {
			return wrapErr(ErrMalformed, "register event missing identity key")
		}
		s.instances[ev.Instance] = &instanceState{
			account:  ev.Account,
			device:   ev.Device,
			instance: ev.Instance,
			active:   true,
			idKey:    clone(ev.IdentityKey),
			revoked:  map[uint32]bool{},
		}
		s.nameToInst[ev.Device] = ev.Instance

	case EvRemove:
		inst, ok := s.instances[ev.Instance]
		if !ok {
			return wrapErr(ErrUnknownInstance, "remove unknown instance %s", ev.Instance)
		}
		inst.active = false

	case EvRotateIDKey:
		inst, ok := s.instances[ev.Instance]
		if !ok || !inst.active {
			return wrapErr(ErrNotAuthorized, "rotate on inactive/unknown instance %s", ev.Instance)
		}
		if len(ev.IdentityKey) == 0 {
			return wrapErr(ErrMalformed, "rotate event missing identity key")
		}
		inst.idKey = clone(ev.IdentityKey)

	case EvOPKBlock:
		inst, ok := s.instances[ev.Instance]
		if !ok || !inst.active {
			return wrapErr(ErrNotAuthorized, "opk block on inactive/unknown instance %s", ev.Instance)
		}
		inst.opkRoot = clone(ev.OPKRoot)
		inst.opkCount = ev.OPKCount
		inst.revoked = map[uint32]bool{} // 新树取代旧树，旧撤销集合清空

	case EvOPKRevoke:
		inst, ok := s.instances[ev.Instance]
		if !ok || !inst.active {
			return wrapErr(ErrNotAuthorized, "revoke on inactive/unknown instance %s", ev.Instance)
		}
		if ev.OPKIndex >= inst.opkCount {
			return wrapErr(ErrMalformed, "revoke index %d out of range %d", ev.OPKIndex, inst.opkCount)
		}
		inst.revoked[ev.OPKIndex] = true

	default:
		return wrapErr(ErrMalformed, "unknown event kind %q", ev.Kind)
	}
	return nil
}

// sessionIdentity 是用于跨分支比较的“会话身份”元组。
// 只有该元组在所有可成立分支上完全相同，会话才被接受。
type sessionIdentity struct {
	account    AccountID
	instance   InstanceID
	idKey      []byte
	opkRoot    []byte
	opkIndex   uint32
	revoked    bool
	active     bool
	commitment []byte
}

func (s *branchState) identityFor(env *SessionEnvelope) (sessionIdentity, error) {
	inst, ok := s.instances[env.Instance]
	ident := sessionIdentity{
		account:    env.Account,
		instance:   env.Instance,
		opkIndex:   env.OPKIndex,
		commitment: clone(env.OPKCommitment),
	}
	if !ok {
		return ident, wrapErr(ErrUnknownInstance, "instance %s absent at checkpoint size %d",
			env.Instance, s.size)
	}
	if inst.account != env.Account {
		return ident, wrapErr(ErrNotAuthorized, "account mismatch for instance %s", env.Instance)
	}
	ident.idKey = clone(inst.idKey)
	ident.opkRoot = clone(inst.opkRoot)
	ident.active = inst.active
	ident.revoked = inst.revoked[env.OPKIndex]
	return ident, nil
}

func (a sessionIdentity) equals(b sessionIdentity) bool {
	return a.account == b.account &&
		a.instance == b.instance &&
		a.opkIndex == b.opkIndex &&
		a.revoked == b.revoked &&
		a.active == b.active &&
		bytes.Equal(a.idKey, b.idKey) &&
		bytes.Equal(a.opkRoot, b.opkRoot) &&
		bytes.Equal(a.commitment, b.commitment)
}

// authorized 判定该会话身份在对应检查点视图下是否仍然获授权：
// 实例活跃、身份钥存在、OPK 树已挂载、序号在范围内且该叶子未被撤销。
func (id sessionIdentity) authorized() error {
	if !id.active {
		return wrapErr(ErrNotAuthorized, "instance %s removed before session", id.instance)
	}
	if len(id.idKey) == 0 {
		return wrapErr(ErrNotAuthorized, "instance %s has no identity key", id.instance)
	}
	if len(id.opkRoot) == 0 {
		return wrapErr(ErrNotAuthorized, "instance %s has no OPK tree at session time", id.instance)
	}
	if id.revoked {
		return wrapErr(ErrNotAuthorized, "OPK index %d revoked before session", id.opkIndex)
	}
	return nil
}

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
