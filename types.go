package netsec

// Violation categories reported by Admit.
const (
	// ViolationForbiddenFunction marks a template function that reads
	// external state and must never appear.
	ViolationForbiddenFunction = "ForbiddenFunction"
	// ViolationLookupSecret marks a lookup call whose kind is "Secret".
	ViolationLookupSecret = "LookupSecret"
	// ViolationDynamicLookup marks a lookup call whose kind cannot be
	// determined statically (dynamic expression, concatenation, missing).
	ViolationDynamicLookup = "DynamicLookupKind"
	// ViolationSecretLeak marks a non-Secret resource whose string field
	// contains a cluster Secret value in plain, base64 or hex form.
	ViolationSecretLeak = "SecretLeak"
)

// FuncCall describes one template function invocation extracted from a
// third-party chart during static analysis.
type FuncCall struct {
	// Name is the template function name, e.g. "lookup" or "env".
	Name string
	// Args holds the call arguments in declaration order.
	Args []Arg
	// Position is an optional template location (e.g.
	// "templates/cm.yaml:12:3") used purely for error reporting.
	Position string
}

// Arg describes one function argument.
type Arg struct {
	// Value holds the static literal value of the argument. When Static is
	// false it may instead contain the source text of the dynamic
	// expression, which is used for violation messages only.
	Value string
	// Static reports whether Value is a compile-time literal. Dynamic
	// arguments (variables, pipelines, string concatenations) set it false.
	Static bool
}

// Resource is one rendered Kubernetes object presented for admission.
type Resource struct {
	Kind      string
	Namespace string
	Name      string
	// Fields contains every string-valued field of the rendered object,
	// including metadata.labels and metadata.annotations values.
	Fields []FieldValue
}

// FieldValue pairs a dotted field path (e.g. "data.config",
// "metadata.annotations.checksum", "spec.containers[0].image") with its
// rendered string value.
type FieldValue struct {
	Path  string
	Value string
}

// SecretValue is one decoded value stored in a cluster Secret.
type SecretValue struct {
	Namespace string
	Name      string
	Key       string
	// Value is the decoded secret data. Binary values are supported.
	Value []byte
}

// SecretFetcher is supplied by the caller and returns the decoded values
// of the cluster Secrets that the rendered workload may reference.
type SecretFetcher func() ([]SecretValue, error)

// Violation describes a single rejected template behavior or leaked field.
type Violation struct {
	// Kind is one of the Violation* constants.
	Kind string
	// Message is a human-readable description.
	Message string

	// Function and Position locate the offending template call (static
	// checks).
	Function string
	Position string

	// Resource coordinates and FieldPath locate the offending rendered
	// field (data checks).
	ResourceKind string
	Namespace    string
	ResourceName string
	FieldPath    string

	// Secret coordinates identify the matched cluster Secret.
	SecretNamespace string
	SecretName      string
	SecretKey       string
	// Encoding labels how the secret value was embedded; see the Encoding*
	// constants. Empty for static-call violations.
	Encoding string
}

// Report is the admission result.
type Report struct {
	// Allowed is true only when Violations is empty.
	Allowed    bool
	Violations []Violation
}
