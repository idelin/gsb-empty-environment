package delegation

import "time"

// Validate 校验 id 的授权在 at 时刻是否有效。
// generation <= 0 表示校验 at 时刻已签发的最新一代。
//
// 校验沿父链向上进行，在距离被校验授权最近的失效点停止，
// 因此返回的 Chain 是“最短失效原因链”。
func (s *Store) Validate(id string, generation int, at time.Time) Result {
	return s.ValidateRef(Ref{ID: id, Generation: generation}, at)
}

// ValidateRef 与 Validate 相同，但接受完整引用。
func (s *Store) ValidateRef(ref Ref, at time.Time) Result {
	var chain []string
	visited := make(map[string]bool)

	cur := s.resolve(ref, at)
	if cur == nil {
		// 区分“从未签发”与“该时刻尚未签发”。
		if recs := s.byID[ref.ID]; len(recs) > 0 && ref.Generation <= 0 {
			return invalid(ReasonNotYetIssued, append(chain, ref.Key()))
		}
		return invalid(ReasonNotFound, append(chain, ref.Key()))
	}

	for {
		key := cur.ref().Key()
		chain = append(chain, key)
		if visited[key] {
			return invalid(ReasonCycle, chain)
		}
		visited[key] = true

		if reason, ok := cur.selfCheck(at); !ok {
			return invalid(reason, chain)
		}

		parentRef := cur.grant.Parent
		if parentRef == nil {
			return Result{Valid: true}
		}
		parent := s.resolveParent(cur)
		if parent == nil {
			missing := *parentRef
			if missing.Generation <= 0 {
				// 父链缺失且未指定代次：记录解析失败的目标 ID。
				chain = append(chain, keyOf(missing.ID, 0))
			} else {
				chain = append(chain, missing.Key())
			}
			return invalid(ReasonParentMissing, chain)
		}
		if reason, ok := containmentCheck(cur, parent); !ok {
			chain = append(chain, parent.ref().Key())
			return invalid(reason, chain)
		}
		cur = parent
	}
}

// selfCheck 检查授权自身在 at 时刻的状态（不涉父链）。
func (r *record) selfCheck(at time.Time) (Reason, bool) {
	if at.Before(r.issuedAt) {
		return ReasonNotYetIssued, false
	}
	if r.revokedAt != nil && !at.Before(*r.revokedAt) {
		return ReasonRevoked, false
	}
	if r.supersededAt != nil && !at.Before(*r.supersededAt) {
		return ReasonSuperseded, false
	}
	if at.Before(r.grant.NotBefore) {
		return ReasonNotYetValid, false
	}
	if !at.Before(r.grant.NotAfter) {
		return ReasonExpired, false
	}
	return "", true
}

// containmentCheck 检查子授权是否被父授权完全包含。
func containmentCheck(child, parent *record) (Reason, bool) {
	if !isSubset(child.grant.Actions, parent.grant.Actions) {
		return ReasonActionsExceedParent, false
	}
	if !isSubset(child.grant.Resources, parent.grant.Resources) {
		return ReasonResourcesExceedParent, false
	}
	if child.grant.NotBefore.Before(parent.grant.NotBefore) ||
		child.grant.NotAfter.After(parent.grant.NotAfter) {
		return ReasonValidityExceedsParent, false
	}
	return "", true
}

func isSubset(child, parent []string) bool {
	set := make(map[string]bool, len(parent))
	for _, p := range parent {
		set[p] = true
	}
	for _, c := range child {
		if !set[c] {
			return false
		}
	}
	return true
}

func invalid(reason Reason, chain []string) Result {
	return Result{Valid: false, Reason: reason, Chain: chain}
}
