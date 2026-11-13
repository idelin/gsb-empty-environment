package netsec

// Convenience aliases for the migration-host vocabulary.

// Write is an alias for WritePage.
func (m *Memory) Write(page PageID, data []byte) (Generation, error) {
	return m.WritePage(page, data)
}

// Snapshot is an alias for SnapshotDirty.
func (m *Memory) Snapshot() (DirtySnapshot, error) {
	return m.SnapshotDirty()
}

// AllPageGenerations returns the current generation of every page.
func (m *Memory) AllPageGenerations() [MaxPages]Generation {
	infos := m.AllPageInfos()
	var out [MaxPages]Generation
	for i := 0; i < MaxPages; i++ {
		out[i] = infos[i].Generation
	}
	return out
}

// PageCount returns the number of valid pages.
func (m *Memory) PageCount() int {
	infos := m.AllPageInfos()
	n := 0
	for i := 0; i < MaxPages; i++ {
		if infos[i].Valid {
			n++
		}
	}
	return n
}

// Stage is an alias for StagePage.
func (t *Target) Stage(rec PageRecord) error {
	return t.StagePage(rec)
}

// Readback is an alias for ReadbackDigest.
func (t *Target) Readback(page PageID) (Digest, error) {
	return t.ReadbackDigest(page)
}

// BeginRound is an alias for Round.
func (m *Migrator) BeginRound() (RoundResult, error) {
	return m.Round()
}

// FreezeAndCutover is an alias for Cutover.
func (m *Migrator) FreezeAndCutover(device DeviceState) (*Handoff, error) {
	return m.Cutover(device)
}

// DeliverReceipt is an alias for StartTarget.
func (m *Migrator) DeliverReceipt() (*StartupConfirmation, error) {
	return m.StartTarget()
}

// Confirm is an alias for ConfirmStartup.
func (m *Migrator) Confirm(conf *StartupConfirmation) error {
	return m.ConfirmStartup(conf)
}

// Rollback is an alias for Abort.
func (m *Migrator) Rollback(reason string) error {
	return m.Abort(reason)
}

// AckStartup is an alias for ConfirmStartup.
func (m *Migrator) AckStartup(conf *StartupConfirmation) error {
	return m.ConfirmStartup(conf)
}

// NewMigration is an alias for NewMigrator.
func NewMigration(src *Memory, target *Target, signer *Signer, opts ...MigratorOption) (*Migrator, error) {
	return NewMigrator(src, target, signer, opts...)
}
