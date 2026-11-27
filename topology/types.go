package topology

// DefaultOverloadThreshold mirrors the Kubernetes endpointslice controller
// default (1.2): an endpoint allocated to a zone must cover at least
// 1/threshold of its expected traffic share.
const DefaultOverloadThreshold = 1.2

// EndpointConditions mirrors the Kubernetes EndpointConditions triplet.
//
// An endpoint with Ready == false and Serving == false is considered failed
// and is never routable. An endpoint with Ready == false, Serving == true and
// Terminating == true is in graceful shutdown: it never receives new traffic
// while ready endpoints exist but acts as a last-resort fallback.
type EndpointConditions struct {
	Ready       bool
	Serving     bool
	Terminating bool
}

// Endpoint is a single routable backend.
type Endpoint struct {
	IP         string
	Zone       string
	Node       string
	Weight     int // <= 0 means unset and is normalized to 1 at compile time
	Conditions EndpointConditions

	// SourceSlice identifies the EndpointSlice that contributed this endpoint.
	// It is retained for diagnostics and does not affect routing.
	SourceSlice string
}

// EndpointSlice is an EndpointSlice-like collection of endpoints.
type EndpointSlice struct {
	Name      string
	Endpoints []Endpoint
}

// NodeCapacity is the CPU capacity of a single node in an availability zone.
type NodeCapacity struct {
	Node        string
	Zone        string
	CPUCapacity float64
}

// ZoneCapacity is the aggregated CPU capacity of one availability zone.
type ZoneCapacity struct {
	Zone string
	CPU  float64
}

// TrafficDistribution values mirror Service.spec.trafficDistribution.
type TrafficDistribution string

const (
	// TrafficDistributionUnspecified keeps legacy behavior; topology hints are
	// still enabled when ServiceConfig.TopologyHintsEnabled is true.
	TrafficDistributionUnspecified TrafficDistribution = ""

	// TrafficDistributionPreferClose enables topology aware hints through the
	// modern Service field.
	TrafficDistributionPreferClose TrafficDistribution = "PreferClose"

	// TrafficDistributionCluster explicitly disables topology aware hints.
	TrafficDistributionCluster TrafficDistribution = "Cluster"
)

// InternalTrafficPolicy mirrors Service.spec.internalTrafficPolicy.
type InternalTrafficPolicy string

const (
	// InternalTrafficPolicyCluster routes cluster traffic to any endpoint.
	InternalTrafficPolicyCluster InternalTrafficPolicy = "Cluster"

	// InternalTrafficPolicyLocal restricts traffic to endpoints on the same node.
	InternalTrafficPolicyLocal InternalTrafficPolicy = "Local"
)

// ServiceConfig is the service-level routing configuration.
type ServiceConfig struct {
	// TrafficDistribution is the modern Service field. PreferClose enables
	// hints; Cluster disables them.
	TrafficDistribution TrafficDistribution

	// TopologyHintsEnabled mirrors the legacy service.kubernetes.io/topology-aware-hints
	// annotation. It is OR-ed with TrafficDistribution == PreferClose.
	TopologyHintsEnabled bool

	// InternalTrafficPolicy selects Cluster (default) or Local routing.
	InternalTrafficPolicy InternalTrafficPolicy

	// OverloadThreshold overrides DefaultOverloadThreshold when > 1.
	OverloadThreshold float64
}

// HintsForZones is the topology hint attached to one endpoint.
type HintsForZones struct {
	ForZones []string
}

// ReasonCode is a precise, machine-readable reason for a routing decision.
type ReasonCode string

const (
	// ReasonOK means hints are active.
	ReasonOK ReasonCode = "OK"

	// ReasonHintsDisabled means the service configuration turns hints off.
	ReasonHintsDisabled ReasonCode = "HintsDisabled"

	// ReasonNoInput means no EndpointSlice input was provided.
	ReasonNoInput ReasonCode = "NoInput"

	// ReasonInsufficientZones means fewer than two active zones have CPU.
	ReasonInsufficientZones ReasonCode = "InsufficientZones"

	// ReasonZeroCPUZone means an active zone reports a zero CPU capacity.
	ReasonZeroCPUZone ReasonCode = "ZeroCPUZone"

	// ReasonEndpointZoneCount means ready endpoints are fewer than active zones.
	ReasonEndpointZoneCount ReasonCode = "EndpointCountBelowZoneCount"

	// ReasonOverload means at least one zone cannot receive its minimum
	// endpoint quota without exceeding the overload threshold.
	ReasonOverload ReasonCode = "MinimumQuotaOverload"

	// ReasonInvalidConfig means a configuration value is invalid.
	ReasonInvalidConfig ReasonCode = "InvalidConfig"
)

// Allocation is the control-plane output.
//
// When Active is true, Hints maps every ready endpoint IP to its forZones
// hint. When a safeguard triggers Active is false, Hints is nil and Reason
// explains the precise degradation cause.
type Allocation struct {
	Active  bool
	Reason  ReasonCode
	Message string

	// Threshold is the effective overload threshold used for the decision.
	Threshold float64

	// Hints maps endpoint IP -> hint. Every hint carries exactly one zone.
	Hints map[string]HintsForZones

	// ReadyEndpoints is the de-duplicated set of ready endpoints considered.
	ReadyEndpoints []Endpoint

	// ZoneShares maps zone -> expected traffic share in (0, 1].
	ZoneShares map[string]float64

	// MinimumEndpoints maps zone -> minimum endpoints required to avoid
	// overload. It is populated even when a safeguard later triggers.
	MinimumEndpoints map[string]int
}

// Backend is one entry of a node's compiled forwarding table.
type Backend struct {
	Endpoint Endpoint
	Weight   int

	// Terminating is true when this backend is a graceful-shutdown fallback.
	Terminating bool

	// SelectedZone is the zone whose hint selected this endpoint; it equals
	// the endpoint zone for local picks and differs for cross-zone fills.
	SelectedZone string
}

// RouteTable is the compiled per-node backend set.
type RouteTable struct {
	Node string
	Zone string

	// Local is true when internalTrafficPolicy == Local was applied.
	Local bool

	// HintsApplied is true when zone hints actually narrowed the selection.
	HintsApplied bool

	// FallbackTerminating is true when no ready endpoint was available in the
	// effective routing set and serving+terminating endpoints were used.
	FallbackTerminating bool

	Backends []Backend
}
