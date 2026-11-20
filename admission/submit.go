package admission

// Submit 原子提交一个批次。它在 CAS 循环内完成全部准入校验：
// 任一票据或引用非法则整批拒绝，不发布任何实例、不消费任何票据；
// 相同批次摘要的重放直接返回首次成功回执，绝不重复消费。
func (h *Host) Submit(batch Batch) (Receipt, error) {
	batchDigest, err := batch.Digest()
	if err != nil {
		return Receipt{}, err
	}
	// 快速幂等路径：回执丢失时无需重新执行校验即可取回原结果。
	if prior, ok := h.store.Snapshot().Receipt(batchDigest); ok {
		return prior, nil
	}

	const maxAttempts = 8
	for attempt := 0; attempt < maxAttempts; attempt++ {
		snap := h.store.Snapshot()
		if snap.generation == 0 {
			return Receipt{}, ErrNotBootstrapped
		}
		// CAS 窗口内可能已由并发提交写入同批次回执，先在快照上确认幂等。
		if prior, ok := snap.Receipt(batchDigest); ok {
			return prior, nil
		}
		next, receipt, err := h.validateAndApply(snap, batch, batchDigest)
		if err != nil {
			return Receipt{}, err
		}
		swapped, err := h.store.CompareAndSwap(snap.version, next)
		if err != nil {
			return Receipt{}, err
		}
		if swapped {
			return receipt, nil
		}
		// CAS 失败说明有并发提交推进了版本；重试前先处理幂等重放。
		if prior, ok := h.store.Snapshot().Receipt(batchDigest); ok {
			return prior, nil
		}
	}
	return Receipt{}, ErrStoreConflict
}

type tentativeNode struct {
	instance Instance
	ticket   Ticket
}

// validateAndApply 在状态快照上校验批次并构造下一状态，不产生任何外部副作用。
func (h *Host) validateAndApply(snap *State, batch Batch, batchDigest Digest) (*State, Receipt, error) {
	if len(batch.Requests) == 0 {
		return nil, Receipt{}, ErrEmptyBatch
	}
	if batch.PolicyVersion != snap.policy.Version {
		return nil, Receipt{}, ErrPolicyVersion
	}
	if batch.Generation != snap.generation {
		return nil, Receipt{}, ErrStaleGeneration
	}

	pending := map[Nonce]tentativeNode{}
	edges := map[Nonce]Nonce{} // 子实例随机数 -> 父实例随机数
	tickets := make([]Ticket, 0, len(batch.Requests))
	usedNames := map[string]bool{}

	// 第一遍：验签并登记所有待创建节点与父子边，此时不解析父实例，
	// 因而尚未提交的前向引用（包括环）都能被完整记录下来。
	for _, req := range batch.Requests {
		ticket := req.Ticket
		if err := ticket.Verify(h.pub); err != nil {
			return nil, Receipt{}, err
		}
		ticketDigest, err := ticket.Digest()
		if err != nil {
			return nil, Receipt{}, err
		}
		if snap.consumed[ticketDigest] {
			return nil, Receipt{}, ErrTicketConsumed
		}
		body := ticket.Body
		if body.Purpose != PurposeSpawn {
			return nil, Receipt{}, ErrWrongPurpose
		}
		if body.PolicyVersion != snap.policy.Version {
			return nil, Receipt{}, ErrPolicyVersion
		}
		if body.Generation != snap.generation {
			return nil, Receipt{}, ErrStaleGeneration
		}
		if req.Name == "" || usedNames[req.Name] {
			return nil, Receipt{}, ErrInstanceExists
		}
		if _, taken := snap.names[req.Name]; taken {
			return nil, Receipt{}, ErrInstanceExists
		}
		if _, held := snap.inflightNames[req.Name]; held && snap.inflightNames[req.Name] != body.ChildNonce {
			return nil, Receipt{}, ErrInstanceExists
		}
		if _, live := snap.instances[body.ChildNonce]; live {
			return nil, Receipt{}, ErrInstanceExists
		}
		if _, dup := pending[body.ChildNonce]; dup {
			return nil, Receipt{}, ErrInstanceExists
		}

		childClaimID := claimIDByDigest(snap.policy, body.ChildClaimDigest)
		childClaim, ok := snap.policy.Tree.Claims[childClaimID]
		if !ok {
			return nil, Receipt{}, ErrUnknownClaim
		}
		childDigest, err := childClaim.Digest()
		if err != nil || childDigest != body.ChildClaimDigest {
			return nil, Receipt{}, ErrBindingMismatch
		}
		reserved, isReserved := snap.inflight[body.ChildNonce]
		if !isReserved || reserved.Name != req.Name || reserved.ClaimID != childClaim.ID {
			return nil, Receipt{}, ErrBindingMismatch
		}

		child := Instance{
			Name:        req.Name,
			Nonce:       body.ChildNonce,
			ClaimID:     childClaim.ID,
			ClaimDigest: childDigest,
			Generation:  snap.generation,
			Parent:      body.ParentNonce,
			Volumes:     append([]VolumePerm(nil), body.Volumes...),
		}
		pending[body.ChildNonce] = tentativeNode{instance: child, ticket: ticket}
		edges[body.ChildNonce] = body.ParentNonce
		usedNames[req.Name] = true
		tickets = append(tickets, ticket)
	}

	// 图结构校验先于父级解析：DFS 灰节点再次命中即存在环，
	// 保证环形前向引用返回 ErrCyclic 而不是被“父缺失”提前短路。
	const (
		colorWhite = 0
		colorGray  = 1
		colorBlack = 2
	)
	color := map[Nonce]int{}
	for nonce := range pending {
		color[nonce] = colorWhite
	}
	var visit func(nonce Nonce) error
	visit = func(nonce Nonce) error {
		if color[nonce] == colorGray {
			return ErrCyclic
		}
		if color[nonce] == colorBlack {
			return nil
		}
		color[nonce] = colorGray
		parentNonce := edges[nonce]
		if _, pendingParent := pending[parentNonce]; pendingParent {
			if err := visit(parentNonce); err != nil {
				return err
			}
		}
		color[nonce] = colorBlack
		return nil
	}
	for nonce := range pending {
		if err := visit(nonce); err != nil {
			return nil, Receipt{}, err
		}
	}

	// 第二遍：自根向下解析每个待创建节点的父级。绑定校验、权限子集、
	// 委托层数与深度都在已无环的视图上递归计算，保证所有票据在同一时刻、
	// 同一视图下有效；父身份来自其他实例树时在此无法解析或无法连通根。
	resolved := map[Nonce]Instance{}
	var resolve func(nonce Nonce) (Instance, error)
	resolve = func(nonce Nonce) (Instance, error) {
		if inst, ok := resolved[nonce]; ok {
			return inst, nil
		}
		node := pending[nonce]
		body := node.ticket.Body
		parent, ok := snap.instances[body.ParentNonce]
		if !ok {
			if future, futureOK := pending[body.ParentNonce]; futureOK {
				resolvedParent, resolveErr := resolve(future.instance.Nonce)
				if resolveErr != nil {
					return Instance{}, resolveErr
				}
				parent, ok = resolvedParent, true
			}
		}
		if !ok {
			return Instance{}, ErrParentNotFound
		}
		parentClaim, ok := snap.policy.Tree.Claims[parent.ClaimID]
		if !ok {
			return Instance{}, ErrUnknownClaim
		}
		childClaim, ok := snap.policy.Tree.Claims[node.instance.ClaimID]
		if !ok {
			return Instance{}, ErrUnknownClaim
		}
		if body.ParentClaimDigest != parent.ClaimDigest {
			return Instance{}, ErrBindingMismatch
		}
		if !containsString(parentClaim.AllowedChildren, childClaim.ID) {
			return Instance{}, ErrUnauthorizedClaim
		}
		// 只能转授父实例持有的卷权限，且不能越过子声明声明的卷集合。
		if !volumesSubset(body.Volumes, parent.Volumes) {
			return Instance{}, ErrPermission
		}
		if !volumesSubset(body.Volumes, childClaim.Volumes) {
			return Instance{}, ErrPermission
		}
		parentRemaining := parentClaim.MaxDelegationDepth - parent.Depth
		if body.RemainingDepth < 0 || body.RemainingDepth > parentRemaining ||
			body.RemainingDepth > childClaim.MaxDelegationDepth {
			return Instance{}, ErrPermission
		}
		node.instance.Depth = parent.Depth + 1
		resolved[nonce] = node.instance
		pending[nonce] = node
		return node.instance, nil
	}
	for nonce := range pending {
		if _, err := resolve(nonce); err != nil {
			return nil, Receipt{}, err
		}
	}

	// 连通性：每个待创建节点沿“批内边 + 已发布父链”都必须到达已认证根，
	// 这同时排除了从另一棵合法树取父身份或卷权限的交叉拼接。
	rootNonce := snap.root.Nonce
	for nonce := range pending {
		switch reachesRoot(edges, snap.instances, nonce, rootNonce) {
		case reachOK:
			continue
		case reachCyclic:
			return nil, Receipt{}, ErrCyclic
		case reachForeign:
			// 父链能解析，却收敛到另一棵树的根或悬空：典型的跨树拼接。
			return nil, Receipt{}, ErrCrossTree
		default:
			return nil, Receipt{}, ErrDisconnected
		}
	}

	next := snap.clone()
	instances := make([]Instance, 0, len(batch.Requests))
	for _, req := range batch.Requests {
		node := pending[req.Ticket.Body.ChildNonce]
		next.instances[node.instance.Nonce] = node.instance
		next.names[node.instance.Name] = node.instance.Nonce
		digest, err := node.ticket.Digest()
		if err != nil {
			return nil, Receipt{}, err
		}
		next.consumed[digest] = true
		delete(next.inflight, node.instance.Nonce)
		delete(next.inflightNames, node.instance.Name)
		instances = append(instances, node.instance)
	}
	receipt := Receipt{Body: ReceiptBody{
		BatchDigest: batchDigest,
		Generation:  snap.generation,
		Batch:       batch,
		Instances:   instances,
		Tickets:     tickets,
	}}
	if err := receipt.Sign(h.priv); err != nil {
		return nil, Receipt{}, err
	}
	next.batchReceipts[batchDigest] = receipt
	return next, receipt, nil
}

type reachResult int

const (
	reachBroken  reachResult = iota // 父链中途缺失
	reachOK                         // 到达本树已认证根
	reachCyclic                     // 父链重复访问，存在环
	reachForeign                    // 收敛到非本树根的悬空链
)

// reachesRoot 沿“批内边 + 已发布父链”自下而上走到已认证根，并区分失败形态。
func reachesRoot(edges map[Nonce]Nonce, live map[Nonce]Instance, start, root Nonce) reachResult {
	seen := map[Nonce]bool{}
	current := start
	for {
		if current == root {
			return reachOK
		}
		if seen[current] {
			return reachCyclic
		}
		seen[current] = true
		if parent, ok := edges[current]; ok {
			current = parent
			continue
		}
		inst, ok := live[current]
		if !ok {
			return reachBroken
		}
		if inst.Nonce == root {
			return reachOK
		}
		if inst.Parent == (Nonce{}) {
			// 已发布实例的父链终止于另一个根：父身份来自另一棵合法树。
			return reachForeign
		}
		current = inst.Parent
	}
}
