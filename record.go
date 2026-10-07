// Package groupx provides a tenant-isolated grouping computation engine.
//
// Isolation guarantees:
//   - Deduplication is scoped per tenant: the same record ID in different
//     tenants never deduplicates against each other.
//   - Caches, checkpoints and watermarks are all maintained per tenant.
//   - Cross-tenant state can only be produced by an explicitly authorized
//     merge, which always writes to a brand-new namespace and never
//     mutates the source tenants.
package groupx

// Record is a single input event routed to a tenant-scoped group.
type Record struct {
	Tenant  string  // owning tenant / namespace
	Group   string  // group-by key inside the tenant
	ID      string  // dedup ID, unique per tenant only
	Payload string  // cached per (tenant, ID)
	Value   float64 // aggregated into the group sum
	Ts      int64   // event time, advances the tenant watermark
	Seq     uint64  // ingestion offset, tracked for checkpoints
}

// Aggregate is the per-(tenant, group) accumulated result.
type Aggregate struct {
	Count       int
	Sum         float64
	LastPayload string
	LastTs      int64
}

func (a Aggregate) clone() Aggregate { return a }
