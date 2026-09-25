package pagebackup

// FaultPoint names an injection point where tests or callers can
// simulate I/O faults and process crashes / power loss.
type FaultPoint string

const (
	// PointExportPagesWritten fires after all page payloads of a backup
	// are written to the export staging dir, before the manifest.
	PointExportPagesWritten FaultPoint = "export.pages_written"
	// PointExportManifestCommit fires after the manifest is written,
	// before the staging dir is atomically renamed into place.
	PointExportManifestCommit FaultPoint = "export.manifest_commit"
	// PointRestoreBackupApplied fires after each backup in the chain
	// has been applied and digest-verified in the restore staging dir.
	PointRestoreBackupApplied FaultPoint = "restore.backup_applied"
	// PointRestoreBeforeCommit fires after the full chain is verified,
	// before the staging dir is atomically renamed to the target.
	PointRestoreBeforeCommit FaultPoint = "restore.before_commit"
)

// FaultInject, when non-nil, is called at every FaultPoint. Returning a
// non-nil error simulates an I/O fault: the running operation aborts and
// cleans up its staging area.
var FaultInject func(point FaultPoint) error

// CrashInject, when non-nil, is called at every FaultPoint after
// FaultInject. It is expected to panic (or os.Exit in a subprocess) to
// simulate a process crash / power cut: no cleanup runs, so the staging
// area is left behind exactly as a real crash would leave it.
var CrashInject func(point FaultPoint)

func inject(p FaultPoint) error {
	if FaultInject != nil {
		if err := FaultInject(p); err != nil {
			return err
		}
	}
	if CrashInject != nil {
		CrashInject(p)
	}
	return nil
}
