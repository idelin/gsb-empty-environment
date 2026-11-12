package netsec

import "time"

// VolumeID uniquely identifies a volume generation. Reusing a volume name
// always produces a new VolumeID, so confirmations can never cross generations.
type VolumeID string

// ReplicaID identifies one persisted copy of the volume's decrypting key.
type ReplicaID string

// Replica describes a durable key copy that must be erased before the volume
// can be considered destroyed.
type Replica struct {
	// ID is unique within a volume generation.
	ID ReplicaID `json:"id"`
	// Location is the persisted location the volume host uses to reach the
	// copy, for example a storage object key.
	Location string `json:"location"`
}

// Lease grants one consumer temporary access to a volume's key material.
// Every active lease must expire or be released before erasure starts.
type Lease struct {
	ID        string    `json:"id"`
	VolumeID  VolumeID  `json:"volume_id"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Active reports whether the lease is still valid at now.
func (l Lease) Active(now time.Time) bool {
	return now.Before(l.ExpiresAt)
}

// volumeManifest is the durable description of one volume generation.
type volumeManifest struct {
	ID        VolumeID  `json:"id"`
	Name      string    `json:"name"`
	Replicas  []Replica `json:"replicas"`
	CreatedAt time.Time `json:"created_at"`
	// Frozen is set as the very first durable step of destruction and never
	// cleared again, even across crashes.
	Frozen bool `json:"frozen"`
}

// Phase labels the position of a destruction transaction in its state
// machine. Phases only advance; there is no transition back to a usable
// volume state.
type Phase string

const (
	// PhaseFreezing means the lease prohibition is being durably installed.
	PhaseFreezing Phase = "freezing"
	// PhaseDraining means new leases are prohibited and active leases are
	// being collected.
	PhaseDraining Phase = "draining"
	// PhaseErasing means key replicas are being erased one by one.
	PhaseErasing Phase = "erasing"
	// PhaseReconciling means the observed replica set is being reconciled
	// against the volume manifest.
	PhaseReconciling Phase = "reconciling"
	// PhaseComplete means every key copy is confirmed erased.
	PhaseComplete Phase = "complete"
)

// destroyRecord is the full durable state of one destruction transaction.
type destroyRecord struct {
	VolumeID VolumeID `json:"volume_id"`
	Name     string   `json:"name"`
	// Epoch increments on every durable state transition and supports
	// ordering during crash recovery.
	Epoch int `json:"epoch"`
	// Phase is the current state-machine phase.
	Phase Phase `json:"phase"`
	// Frozen mirrors the manifest prohibition and is always true once a
	// transaction exists.
	Frozen bool `json:"frozen"`
	// Erased contains every replica for which an erasure confirmation has
	// been collected.
	Erased map[ReplicaID]bool `json:"erased"`
	// UnknownReplicas records observed copies that are not part of the
	// manifest and still need an erasure confirmation.
	UnknownReplicas map[ReplicaID]bool `json:"unknown_replicas"`
	// UnknownErased records non-manifest copies whose erasure has been
	// confirmed; they no longer block completion.
	UnknownErased map[ReplicaID]bool `json:"unknown_erased"`
	// ObservedAbsent records manifest replicas that an external scan proved
	// absent; an absent copy counts as erased.
	ObservedAbsent map[ReplicaID]bool `json:"observed_absent"`
	StartedAt      time.Time          `json:"started_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
	CompletedAt    *time.Time         `json:"completed_at,omitempty"`
}

// Status is the externally observable outcome of a destruction transaction.
type Status string

const (
	// StatusInProgress means the transaction is still advancing normally.
	StatusInProgress Status = "in_progress"
	// StatusIncomplete means destruction cannot finish yet, typically because
	// an unknown replica is outstanding.
	StatusIncomplete Status = "incomplete"
	// StatusComplete means every key copy is confirmed erased.
	StatusComplete Status = "complete"
)

// DestructionStatus is a point-in-time snapshot of a destruction transaction.
type DestructionStatus struct {
	VolumeID        VolumeID
	Name            string
	Phase           Phase
	Outcome         Status
	Erased          []ReplicaID
	PendingReplicas []ReplicaID
	UnknownReplicas []ReplicaID
	Epoch           int
}
