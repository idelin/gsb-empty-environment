// Package netsec binds a Fulcio-issued certificate to the workload identity
// that signed an artifact.
//
// Signature verification must not trust subjectAltName alone. A workflow with
// the same name can exist in a different repository, so this package reads the
// Fulcio identity extensions under 1.3.6.1.4.1.57264.1.x and requires the
// caller's policy to match the OIDC issuer, build signer, source repository,
// source digest, ref, and triggering event.
//
// The entry point for a verification flow is VerifyIdentity:
//
//	policy := netsec.Policy{
//		Issuer:                 "https://token.actions.githubusercontent.com",
//		BuildSignerURI:         "https://github.com/acme/app/.github/workflows/release.yml@refs/tags/v1.2.3",
//		SourceRepositoryURI:    "https://github.com/acme/app",
//		SourceRepositoryDigest: "sha256:...",
//		SourceRepositoryRef:    "refs/tags/v1.2.3",
//		BuildTrigger:           "push",
//		RequireTagRef:          true,
//	}
//	identity, err := netsec.VerifyIdentity(cert, policy)
//
// ExtractIdentity can be used when a caller needs the claims but performs its
// own policy comparison.
package netsec
