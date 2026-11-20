package admission

import "crypto/ed25519"

// Host 是消息宿主的请求准入器，所有票据与回执均由其证明密钥背书。
type Host struct {
	store Store
	pub   ed25519.PublicKey
	priv  ed25519.PrivateKey
}

// NewHost 用宿主存储和证明密钥对创建准入器。
func NewHost(store Store, pub ed25519.PublicKey, priv ed25519.PrivateKey) *Host {
	return &Host{store: store, pub: pub, priv: priv}
}

// PublicKey 返回宿主证明公钥（独立快照校验方需要它）。
func (h *Host) PublicKey() ed25519.PublicKey { return h.pub }

// CurrentState 读取宿主当前状态快照。
func (h *Host) CurrentState() *State { return h.store.Snapshot() }

// Bootstrap 用已认证启动度量、声明树策略和根实例建立信任锚。
func (h *Host) Bootstrap(boot BootMeasurement, policy Policy, rootName string, generation uint64) (RootEvidence, error) {
	if generation == 0 {
		return RootEvidence{}, ErrGenerationRollback
	}
	if err := policy.Validate(); err != nil {
		return RootEvidence{}, err
	}
	if boot.PolicyVersion != policy.Version {
		return RootEvidence{}, ErrBootMismatch
	}
	rootDigest, err := policy.ClaimDigest(policy.Tree.Root)
	if err != nil {
		return RootEvidence{}, err
	}
	if boot.RootClaimDigest != rootDigest {
		return RootEvidence{}, ErrBootMismatch
	}
	rootNonce, err := NewNonce()
	if err != nil {
		return RootEvidence{}, err
	}
	evidence := RootEvidence{Body: RootEvidenceBody{
		Boot:              boot,
		Policy:            policy,
		RootNonce:         rootNonce,
		RootName:          rootName,
		InitialGeneration: generation,
	}}
	if err := evidence.Sign(h.priv); err != nil {
		return RootEvidence{}, err
	}
	rootClaim := policy.Tree.Claims[policy.Tree.Root]
	state := emptyState()
	state.generation = generation
	state.policy = policy
	state.rootEvidence = evidence
	state.root = Instance{
		Name:        rootName,
		Nonce:       rootNonce,
		ClaimID:     rootClaim.ID,
		ClaimDigest: rootDigest,
		Generation:  generation,
		Volumes:     append([]VolumePerm(nil), rootClaim.Volumes...),
		Depth:       0,
	}
	state.instances[rootNonce] = state.root
	state.names[rootName] = rootNonce
	if err := h.store.Replace(state); err != nil {
		return RootEvidence{}, err
	}
	return evidence, nil
}

// Restore 在快照恢复后以严格递增的代次重建信任锚。
// 旧代次票据因代次绑定立即失效；同名工作负载会拿到新的实例随机数，
// 因此旧策略/旧代次签出的请求无法借同名重建再次生效。
func (h *Host) Restore(boot BootMeasurement, policy Policy, rootName string, generation uint64) (RootEvidence, error) {
	current := h.store.Snapshot()
	if current.generation != 0 && generation <= current.generation {
		return RootEvidence{}, ErrGenerationRollback
	}
	return h.Bootstrap(boot, policy, rootName, generation)
}

// CheckoutRequest 是父实例签出子实例票据的请求参数。
type CheckoutRequest struct {
	ParentNonce    Nonce
	ChildClaimID   string
	ChildNonce     Nonce
	Purpose        Purpose
	Volumes        []VolumePerm
	RemainingDepth int
}

// PreregisterInstance 为一个尚未提交的子工作负载预留实例随机数与名称。
// 这样父实例就可以在批次提交前为“未来的子节点”签出票据，实现前向引用；
// 预留记录在提交成功时转为正式实例，在放弃时由 ReleaseInstance 回收。
func (h *Host) PreregisterInstance(name string, nonce Nonce, claimID string, depth int, volumes []VolumePerm) (Instance, error) {
	state := h.store.Snapshot()
	if state.generation == 0 {
		return Instance{}, ErrNotBootstrapped
	}
	claim, ok := state.policy.Tree.Claims[claimID]
	if !ok {
		return Instance{}, ErrUnknownClaim
	}
	if depth < 0 || depth > claim.MaxDelegationDepth {
		return Instance{}, ErrPermission
	}
	if !volumesSubset(volumes, claim.Volumes) {
		return Instance{}, ErrPermission
	}
	if nonce == (Nonce{}) {
		return Instance{}, ErrBindingMismatch
	}
	if _, exists := state.instances[nonce]; exists {
		return Instance{}, ErrInstanceExists
	}
	if _, reserved := state.inflight[nonce]; reserved {
		return Instance{}, ErrInstanceExists
	}
	if _, taken := state.names[name]; taken {
		return Instance{}, ErrInstanceExists
	}
	if _, held := state.inflightNames[name]; held {
		return Instance{}, ErrInstanceExists
	}
	digest, err := claim.Digest()
	if err != nil {
		return Instance{}, err
	}
	inst := Instance{
		Name:        name,
		Nonce:       nonce,
		ClaimID:     claim.ID,
		ClaimDigest: digest,
		Generation:  state.generation,
		Depth:       depth,
		Volumes:     append([]VolumePerm(nil), volumes...),
	}
	next := state.clone()
	next.inflight[nonce] = inst
	next.inflightNames[name] = nonce
	if _, err := h.store.CompareAndSwap(state.version, next); err != nil {
		return Instance{}, err
	}
	return inst, nil
}

// ReleaseInstance 回收未使用的预登记实例。
func (h *Host) ReleaseInstance(nonce Nonce) error {
	state := h.store.Snapshot()
	inst, ok := state.inflight[nonce]
	if !ok {
		return ErrParentNotFound
	}
	next := state.clone()
	delete(next.inflight, nonce)
	delete(next.inflightNames, inst.Name)
	_, err := h.store.CompareAndSwap(state.version, next)
	return err
}

// CheckoutTicket 代表父实例（由宿主背书）签出一张委托票据。
// 签出阶段强制“只能转授自己的权限”：卷权限必须被父实例覆盖，
// 剩余委托层数不得超过父实例，子声明必须是父声明允许创建的节点。
func (h *Host) CheckoutTicket(req CheckoutRequest) (Ticket, error) {
	state := h.store.Snapshot()
	if state.generation == 0 {
		return Ticket{}, ErrNotBootstrapped
	}
	parent, ok := state.instances[req.ParentNonce]
	if !ok {
		// 父实例允许是同批尚未提交、已预登记的前向引用。
		parent, ok = state.inflight[req.ParentNonce]
		if !ok {
			return Ticket{}, ErrParentNotFound
		}
	}
	parentClaim, ok := state.policy.Tree.Claims[parent.ClaimID]
	if !ok {
		return Ticket{}, ErrUnknownClaim
	}
	childClaim, ok := state.policy.Tree.Claims[req.ChildClaimID]
	if !ok {
		return Ticket{}, ErrUnknownClaim
	}
	if !containsString(parentClaim.AllowedChildren, childClaim.ID) {
		return Ticket{}, ErrUnauthorizedClaim
	}
	childDigest, err := childClaim.Digest()
	if err != nil {
		return Ticket{}, err
	}
	if req.Purpose != PurposeSpawn {
		return Ticket{}, ErrWrongPurpose
	}
	if req.ChildNonce == (Nonce{}) {
		return Ticket{}, ErrBindingMismatch
	}
	parentRemaining := parentClaim.MaxDelegationDepth - parent.Depth
	if req.RemainingDepth < 0 || req.RemainingDepth > parentRemaining {
		return Ticket{}, ErrPermission
	}
	if req.RemainingDepth > childClaim.MaxDelegationDepth {
		return Ticket{}, ErrPermission
	}
	if !volumesSubset(req.Volumes, parent.Volumes) {
		return Ticket{}, ErrPermission
	}
	if !volumesSubset(req.Volumes, childClaim.Volumes) {
		return Ticket{}, ErrPermission
	}
	ticket := Ticket{Body: TicketBody{
		ParentClaimDigest: parent.ClaimDigest,
		ChildClaimDigest:  childDigest,
		ParentNonce:       parent.Nonce,
		ChildNonce:        req.ChildNonce,
		Generation:        state.generation,
		PolicyVersion:     state.policy.Version,
		Purpose:           req.Purpose,
		Volumes:           append([]VolumePerm(nil), req.Volumes...),
		RemainingDepth:    req.RemainingDepth,
	}}
	if err := ticket.Sign(h.priv); err != nil {
		return Ticket{}, err
	}
	return ticket, nil
}
