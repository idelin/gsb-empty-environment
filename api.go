package credentialauth

// Prove is an alias for BuildProof.
func Prove(snapshot Snapshot) (*Proof, error) {
	return BuildProof(snapshot)
}

// Verify is a package-level alias for BuildProof.  It verifies all merge
// invariants and returns the deterministic proof; use Verifier for stateful
// issuance replay protection.
func Verify(snapshot Snapshot) (*Proof, error) {
	return BuildProof(snapshot)
}

// IssuePermit is a package-level alias for VerifyAndIssue.
func IssuePermit(snapshot Snapshot, request PermitRequest) (*Proof, error) {
	return VerifyAndIssue(snapshot, request)
}

// CheckSnapshot validates a snapshot without returning proof data.
func CheckSnapshot(snapshot Snapshot) error {
	_, err := BuildProof(snapshot)
	return err
}

// MergeParts assembles a snapshot from its three primary sections.
func MergeParts(devices []DeviceReceipt, batches []BatchSummary, reservations []Reservation) (*Proof, error) {
	return BuildProof(Snapshot{
		Devices:      devices,
		Batches:      batches,
		Reservations: reservations,
	})
}

// MergePartsWithRequest assembles a snapshot and issues a lock for request.
func MergePartsWithRequest(devices []DeviceReceipt, batches []BatchSummary, reservations []Reservation, request PermitRequest) (*Proof, error) {
	return VerifyAndIssue(Snapshot{
		Devices:      devices,
		Batches:      batches,
		Reservations: reservations,
	}, request)
}

// DeviceState is an alias for DeviceReceipt.
type DeviceState = DeviceReceipt

// BatchLog is an alias for BatchSummary.
type BatchLog = BatchSummary

// Lock is an alias for Permit.
type Lock = Permit
