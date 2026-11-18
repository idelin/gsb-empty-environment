// Package netsec implements admission-time selection and evaluation of
// Notation-style image trust policies.
//
// Registry scopes have the form registry/repository, with an optional trailing
// "/*" wildcard. The wildcard does not cross a repository path boundary and is
// never interpreted in the middle of a scope. A concrete scope takes
// precedence over the global "*" scope; an image matching more than one
// concrete scope is a configuration error.
//
// Signature verification is fail-closed by default. Strict mode denies
// expired signatures and unknown revocation status. Permissive mode allows
// only an unknown revocation status. Audit mode records findings without
// denying admission. Signatures must bind the resolved image digest; a tag
// alone is never accepted.
package netsec
