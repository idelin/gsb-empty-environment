package material

// Publish 原子发布一个冻结批次：先用版本图核对整个批次，
// 任一版本缺失、未签收、已撤回，或依赖落在冻结集合之外
// （混用新旧签收），整个批次失败，不产生任何部分状态；
// 全部通过后才逐条置为已发布。
func (l *Library) Publish(batch []Ref) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.snapshotLocked().VerifyFrozenSet(batch); err != nil {
		return err
	}
	for _, ref := range batch {
		l.items[ref.EvidenceID].Versions[ref.Version].Published = true
	}
	return nil
}

// Published 查询指定版本是否已发布。
func (l *Library) Published(id string, version int) (bool, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	rec, err := l.recordLocked(id, version)
	if err != nil {
		return false, err
	}
	return rec.Published, nil
}
