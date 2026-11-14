package netsec

// TrustLevel is the repository trust level maintained by the scheduling
// service. It is never accepted from an untrusted job payload.
type TrustLevel uint8

const (
	// TrustUntrusted is for forks, unknown repositories, and untrusted code.
	TrustUntrusted TrustLevel = iota
	// TrustTrusted is for code from a trusted internal repository.
	TrustTrusted
	// TrustPrivileged is reserved for protected branches of repositories
	// allowed to use production executors and privileged capabilities.
	TrustPrivileged
)

func (t TrustLevel) valid() bool {
	return t <= TrustPrivileged
}

func (t TrustLevel) String() string {
	switch t {
	case TrustUntrusted:
		return "untrusted"
	case TrustTrusted:
		return "trusted"
	case TrustPrivileged:
		return "privileged"
	default:
		return "invalid"
	}
}

// Source describes where the job definition came from. The scheduling control
// plane must populate this from trusted provenance metadata.
type Source string

const (
	SourceUnknown           Source = "unknown"
	SourceFork              Source = "fork"
	SourceRepository        Source = "repository"
	SourceTrustedAutomation Source = "trusted_automation"
)

// TrustDomain is the isolation boundary declared by an executor when it
// registers. Jobs may only be placed in the one domain derived for them.
type TrustDomain string

const (
	DomainUntrusted  TrustDomain = "untrusted"
	DomainTrusted    TrustDomain = "trusted"
	DomainProduction TrustDomain = "production"
)

// Capability is a hard requirement advertised by an executor.
type Capability string

// Repository is the scheduler-owned trust policy for a repository.
type Repository struct {
	ID                string
	Trust             TrustLevel
	ProtectedBranches []string
}

// Executor is an executor registration. Domain is the trust boundary the
// executor belongs to and cannot be changed by re-registration.
type Executor struct {
	ID           string
	Domain       TrustDomain
	Labels       []string
	Capabilities []Capability
}

// Job is the information the scheduler passes to the router. Labels and
// capabilities are requests only. Repository, Source, Ref, and Branch must
// originate from trusted control-plane metadata and not from user-authored
// workflow content.
type Job struct {
	ID                   string
	Repository           string
	Source               Source
	Branch               string
	Ref                  string
	Labels               []string
	RequiredCapabilities []Capability

	// Queue is an optional logical queue name. The placement always prefixes
	// it with the job's trust domain.
	Queue string
	// CacheKey and ArtifactKey are optional suffixes. Placement namespaces
	// always include the trust domain and repository.
	CacheKey    string
	ArtifactKey string
}

// Config controls the trust policy of a Router. If Domains is nil, the
// built-in untrusted/trusted/production mapping is used.
type Config struct {
	// PrivilegedLabels are the only labels that require privileged
	// authorization. Unknown labels are ordinary selectors.
	PrivilegedLabels []string
	// PrivilegedCapabilities are capabilities that require privileged
	// authorization in addition to a matching executor.
	PrivilegedCapabilities []Capability
	// Domains maps every trust level to a distinct isolation domain.
	Domains map[TrustLevel]TrustDomain
	// Repositories preloads trusted repository policy.
	Repositories []Repository
}

// Decision is the resolved trust decision for a job before executor selection.
type Decision struct {
	Repository      string
	RepositoryKnown bool
	Source          Source
	Ref             string
	Branch          string
	ProtectedBranch bool
	Trust           TrustLevel
	Domain          TrustDomain
}

// Placement is the only successful result of routing. Executor, Domain, and
// the three scope strings describe one mutually consistent placement.
type Placement struct {
	ExecutorID        string
	Executor          Executor
	Domain            TrustDomain
	Trust             TrustLevel
	ProtectedBranch   bool
	Queue             string
	CacheNamespace    string
	ArtifactNamespace string
}
