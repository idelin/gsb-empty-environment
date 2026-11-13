package netsec

import "errors"

var (
	// ErrInvalidDefinition means the function definition is structurally
	// incomplete or internally inconsistent.
	ErrInvalidDefinition = errors.New("netsec: invalid function definition")

	// ErrInvalidSearchPath is returned when a fixed search path cannot be
	// represented entirely by schema IDs.
	ErrInvalidSearchPath = errors.New("netsec: invalid search path")

	// ErrUnsafeSystemSchema means the first search-path entry is not a
	// read-only system schema.
	ErrUnsafeSystemSchema = errors.New("netsec: first search-path schema is not a read-only system schema")

	// ErrTemporarySchema means a temporary schema or caller-controlled
	// search-path marker was supplied.
	ErrTemporarySchema = errors.New("netsec: temporary or caller-controlled schema is forbidden")

	// ErrPublicWritableSchema means a schema can be written by every role.
	ErrPublicWritableSchema = errors.New("netsec: PUBLIC-writable schema is forbidden")

	// ErrWritableSchema means a search-path schema can be written by somebody
	// other than the function definer.
	ErrWritableSchema = errors.New("netsec: search-path schema is writable by a non-definer")

	// ErrUnresolvedReference means no catalog object matched a reference.
	ErrUnresolvedReference = errors.New("netsec: unresolved object reference")

	// ErrAmbiguousReference means more than one object matched an
	// unqualified or overloaded reference.
	ErrAmbiguousReference = errors.New("netsec: ambiguous object reference")

	// ErrOutsideSearchPath means an explicitly qualified reference does not
	// point into the function's frozen search path.
	ErrOutsideSearchPath = errors.New("netsec: reference is outside the frozen search path")

	// ErrDynamicSQLDenied means a function attempted to execute dynamic SQL
	// without a caller-supplied trusted AST.
	ErrDynamicSQLDenied = errors.New("netsec: dynamic SQL denied")

	// ErrCallerDenied means the caller is not in the function's caller set.
	ErrCallerDenied = errors.New("netsec: caller is not allowed to invoke function")

	// ErrStaleObject means an object disappeared, changed version, or no
	// longer has the identity that was frozen at publish time.
	ErrStaleObject = errors.New("netsec: frozen object is stale")

	// ErrFunctionRevoked means the published function was revoked (or its
	// underlying function object was dropped).
	ErrFunctionRevoked = errors.New("netsec: function is revoked")

	// ErrUnsafeSchemaAtExecution means a schema passed validation at publish
	// time but no longer satisfies the frozen search-path policy.
	ErrUnsafeSchemaAtExecution = errors.New("netsec: search-path schema became unsafe")

	// ErrInvalidCatalog means the catalog is missing required metadata.
	ErrInvalidCatalog = errors.New("netsec: invalid catalog")

	// ErrRecursiveFunction means nested SECURITY DEFINER execution exceeded
	// the recursion guard.
	ErrRecursiveFunction = errors.New("netsec: recursive function invocation")
)
