package crush

// SlotStat records deterministic retry/fallback information for one requested
// replica slot.
type SlotStat struct {
	Slot     int `json:"slot"`
	Index    int `json:"index"`
	Position int `json:"position"`

	OSDID  int `json:"osd_id"`
	OSD    int `json:"osd"`
	ID     int `json:"id"`
	NodeID int `json:"node_id"`

	OK       bool `json:"ok"`
	Selected bool `json:"selected"`

	Conflict      bool `json:"conflict"`
	HasConflict   bool `json:"has_conflict"`
	IsConflict    bool `json:"is_conflict"`
	Duplicate     bool `json:"duplicate"`
	Fallback      bool `json:"fallback"`
	HasFallback   bool `json:"has_fallback"`
	IsFallback    bool `json:"is_fallback"`
	UsedFallback  bool `json:"used_fallback"`
	OuterFallback bool `json:"outer_fallback"`
	LocalRetry    bool `json:"local_retry"`
	Retried       bool `json:"retried"`
	Down          bool `json:"down"`
	ZeroWeight    bool `json:"zero_weight"`
	NoLeaf        bool `json:"no_leaf"`

	ConflictCount      int `json:"conflict_count"`
	Conflicts          int `json:"conflicts"`
	DuplicateCount     int `json:"duplicate_count"`
	FallbackCount      int `json:"fallback_count"`
	Fallbacks          int `json:"fallbacks"`
	OuterFallbackCount int `json:"outer_fallback_count"`
	RetryCount         int `json:"retry_count"`
	Retries            int `json:"retries"`
	Attempts           int `json:"attempts"`
	DownCount          int `json:"down_count"`
	ZeroWeightCount    int `json:"zero_weight_count"`
	NoLeafCount        int `json:"no_leaf_count"`

	Reasons []string `json:"reasons,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// Result is a PG mapping and per-slot diagnostics.
type Result struct {
	PGID     uint64 `json:"pg_id"`
	PG       uint64 `json:"pg"`
	ID       uint64 `json:"id"`
	RuleName string `json:"rule_name"`
	Rule     string `json:"rule"`

	OSDs         []int `json:"osds"`
	OSD          []int `json:"osd"`
	Osds         []int `json:"osds_alt,omitempty"`
	OSDIDs       []int `json:"osd_ids"`
	SelectedOSDs []int `json:"selected_osds"`
	Placement    []int `json:"placement"`
	Out          []int `json:"out,omitempty"`
	Output       []int `json:"output,omitempty"`
	FinalOSDs    []int `json:"final_osds,omitempty"`

	Slots      []SlotStat `json:"slots"`
	Stats      []SlotStat `json:"stats"`
	SlotStats  []SlotStat `json:"slot_stats"`
	Statistics []SlotStat `json:"statistics,omitempty"`

	ConflictSlots []int `json:"conflict_slots,omitempty"`
	FallbackSlots []int `json:"fallback_slots,omitempty"`
	MissingSlots  []int `json:"missing_slots,omitempty"`

	ConflictCount int `json:"conflict_count"`
	FallbackCount int `json:"fallback_count"`
	MissingCount  int `json:"missing_count"`
}

// HasHole reports whether an independent slot is -1.
func (r *Result) HasHole() bool {
	if r == nil {
		return true
	}
	for _, id := range r.OSDs {
		if id < 0 {
			return true
		}
	}
	return false
}

// OSDList returns the final OSD slice.
func (r *Result) OSDList() []int {
	if r == nil {
		return nil
	}
	return r.OSDs
}

type (
	Mapping       = Result
	MapResult     = Result
	MappingResult = Result
	Placement     = Result
	SlotStats     = SlotStat
	SlotResult    = SlotStat
	SlotInfo      = SlotStat
	Stat          = SlotStat
)
