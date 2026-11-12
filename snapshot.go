package netsec

// snapshotResourceLocked 在持锁状态下构造资源快照，
// 所有切片都复制并排序，保证对外视图稳定可比较。
func (idx *DependencyIndex) snapshotResourceLocked(entry *resourceEntry) *Resource {
	causes := cloneVersions(entry.causes)
	if len(causes) == 0 {
		causes = []ExceptionVersion{}
	}
	return &Resource{
		ID:                entry.id,
		ExceptionDeps:     cloneVersions(entry.excDeps),
		UpstreamDeps:      cloneStrings(entry.upstreams),
		Restricted:        entry.restricted,
		TerminationPolicy: entry.policy,
		Causes:            causes,
	}
}
