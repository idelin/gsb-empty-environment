package quota

// TransferOwnership 将物理块归属从 from 原子迁移到 to：
// 计费字节在同一把锁内完成「校验限额 → 源减 → 目标加 → 改归属」，
// 不存在中间态。共享块此后只向 to 计费。
//
// 幂等：同一 key 重放返回首次结果；若块已归属 to，视为成功空操作。
func (s *Store) TransferOwnership(key, blockID, from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ikey := "transfer/" + key
	if r, ok := s.idem[ikey]; ok {
		return r.err
	}
	err := s.transferLocked(blockID, from, to)
	s.idem[ikey] = &opResult{err: err}
	return err
}

func (s *Store) transferLocked(blockID, from, to string) error {
	if from == "" || to == "" {
		return ErrInvalidArgument
	}
	m, ok := s.blocks[blockID]
	if !ok {
		return ErrBlockNotFound
	}
	if m.owner == to {
		return nil // 已归属目标：幂等空操作
	}
	if m.owner != from {
		return ErrBlockOwnerMismatch
	}
	// 目标租户需容纳迁入字节（全局总量不变，无需校验全局）。
	if limit, ok := s.tenantLimit[to]; ok {
		if s.reserved[to]+s.used[to]+m.size > limit {
			return ErrTenantLimitExceeded
		}
	}
	s.used[from] -= m.size
	s.used[to] += m.size
	s.blocks[blockID] = blockMeta{owner: to, size: m.size}
	s.appendLocked(Event{
		Type:   EventTransfer,
		Tenant: from,
		To:     to,
		ResID:  blockID,
		Bytes:  m.size,
	})
	return nil
}
