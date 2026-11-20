package admission

import "crypto/ed25519"

// VerifySnapshot 是不依赖宿主可变状态的独立快照校验函数。
// 它只信任宿主证明公钥，从根证据、已发布实例和签名收据重建完整授权链：
// 验证信任锚、每张票据的签名与绑定、收据签名与批次摘要，并确认所有实例
// 构成一棵从已认证根出发的无环树，且每张票据恰好被消费一次。
func VerifySnapshot(snap Snapshot, hostKey ed25519.PublicKey) error {
	if len(snap.Instances) == 0 {
		return ErrSnapshotEmpty
	}

	// 1. 信任锚：根证据签名、启动度量与策略/根声明的绑定。
	evidence := snap.Root
	if err := evidence.Verify(hostKey); err != nil {
		return err
	}
	policy := snap.Policy
	if err := policy.Validate(); err != nil {
		return err
	}
	if evidence.Body.Policy.Version != policy.Version {
		return ErrPolicyVersion
	}
	rootDigest, err := policy.ClaimDigest(policy.Tree.Root)
	if err != nil {
		return err
	}
	boot := evidence.Body.Boot
	if boot.PolicyVersion != policy.Version || boot.RootClaimDigest != rootDigest {
		return ErrBootMismatch
	}

	// 2. 已发布实例索引化，并核对根实例与信任锚一致。
	live := map[Nonce]Instance{}
	names := map[string]bool{}
	var root Instance
	for _, inst := range snap.Instances {
		if _, dup := live[inst.Nonce]; dup {
			return ErrInstanceExists
		}
		if names[inst.Name] {
			return ErrInstanceExists
		}
		live[inst.Nonce] = inst
		names[inst.Name] = true
		claim, ok := policy.Tree.Claims[inst.ClaimID]
		if !ok {
			return ErrUnknownClaim
		}
		digest, err := claim.Digest()
		if err != nil || digest != inst.ClaimDigest {
			return ErrBindingMismatch
		}
		if inst.Nonce == evidence.Body.RootNonce {
			root = inst
		}
	}
	if root.Nonce != evidence.Body.RootNonce || root.Name != evidence.Body.RootName ||
		root.Generation != evidence.Body.InitialGeneration || root.Parent != (Nonce{}) {
		return ErrBootMismatch
	}
	if len(snap.Receipts) == 0 {
		if len(snap.Instances) != 1 {
			return ErrSnapshotCoverage
		}
		return nil
	}

	// 3. 逐张收据验签，并重建票据消费记录与实例创建记录。
	consumed := map[Digest]bool{}
	created := map[Nonce]bool{}
	edges := map[Nonce]Nonce{}
	gen := root.Generation
	for _, receipt := range snap.Receipts {
		if err := receipt.Verify(hostKey); err != nil {
			return err
		}
		body := receipt.Body
		if body.Generation != gen {
			return ErrStaleGeneration
		}
		digest, err := body.Batch.Digest()
		if err != nil || digest != body.BatchDigest {
			return ErrBindingMismatch
		}
		if len(body.Tickets) != len(body.Batch.Requests) ||
			len(body.Instances) != len(body.Batch.Requests) {
			return ErrSnapshotCoverage
		}
		for i, ticket := range body.Tickets {
			if err := ticket.Verify(hostKey); err != nil {
				return err
			}
			ticketDigest, err := ticket.Digest()
			if err != nil {
				return err
			}
			if consumed[ticketDigest] {
				return ErrReceiptReplay
			}
			consumed[ticketDigest] = true

			tb := ticket.Body
			if tb.Generation != gen || tb.PolicyVersion != policy.Version {
				return ErrStaleGeneration
			}
			req := body.Batch.Requests[i]
			reqDigest, err := req.Ticket.Digest()
			if err != nil || reqDigest != ticketDigest {
				return ErrBindingMismatch
			}
			child := body.Instances[i]
			if child.Nonce != tb.ChildNonce || child.ClaimDigest != tb.ChildClaimDigest ||
				child.Parent != tb.ParentNonce || child.Generation != gen {
				return ErrBindingMismatch
			}
			published, ok := live[child.Nonce]
			if !ok || published.Name != req.Name || !sameVolumes(published.Volumes, tb.Volumes) {
				return ErrSnapshotCoverage
			}
			if created[child.Nonce] {
				return ErrReceiptReplay
			}
			created[child.Nonce] = true
			edges[child.Nonce] = tb.ParentNonce

			parent, ok := live[tb.ParentNonce]
			if !ok {
				return ErrParentNotFound
			}
			parentClaim, ok := policy.Tree.Claims[parent.ClaimID]
			if !ok {
				return ErrUnknownClaim
			}
			childClaimID := claimIDByDigest(policy, tb.ChildClaimDigest)
			childClaim, ok := policy.Tree.Claims[childClaimID]
			if !ok {
				return ErrUnknownClaim
			}
			if tb.ParentClaimDigest != parent.ClaimDigest {
				return ErrBindingMismatch
			}
			if !containsString(parentClaim.AllowedChildren, childClaim.ID) {
				return ErrUnauthorizedClaim
			}
			if !volumesSubset(tb.Volumes, parent.Volumes) ||
				!volumesSubset(tb.Volumes, childClaim.Volumes) {
				return ErrPermission
			}
			parentRemaining := parentClaim.MaxDelegationDepth - parent.Depth
			if tb.RemainingDepth < 0 || tb.RemainingDepth > parentRemaining ||
				tb.RemainingDepth > childClaim.MaxDelegationDepth ||
				published.Depth != parent.Depth+1 {
				return ErrPermission
			}
			if tb.Purpose != PurposeSpawn {
				return ErrWrongPurpose
			}
		}
	}

	// 4. 覆盖一致：除根外的每个已发布实例都必须由某张收据创建。
	if len(created) != len(snap.Instances)-1 {
		return ErrSnapshotCoverage
	}

	// 5. 图一致：所有实例沿父链无环地收敛到已认证根；
	// 来自另一棵树的父身份或卷权限拼接会在此断开。
	for nonce := range live {
		if nonce == root.Nonce {
			continue
		}
		if reachesRoot(edges, live, nonce, root.Nonce) != reachOK {
			return ErrDisconnected
		}
	}
	return nil
}
