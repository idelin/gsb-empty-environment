package netsec

import "errors"

// Sentinel errors returned by the verifier. Callers can use errors.Is to
// distinguish the exact failure reason when deciding how to log or reject a
// mirrored podspec.
var (
	// ErrInvalidPodspec is returned when the podspec JSON cannot be parsed
	// or is missing a required field.
	ErrInvalidPodspec = errors.New("netsec: invalid podspec")

	// ErrSpecPathMismatch is returned when the on-disk path does not match
	// the Specs/<md5[0]>/<md5[1]>/<md5[2]>/<Name>/<Version>/<Name>.podspec.json
	// layout derived from the pod name and version.
	ErrSpecPathMismatch = errors.New("netsec: podspec path does not match MD5 sharding")

	// ErrChecksumMismatch is returned when the SHA-1 of the podspec bytes
	// does not equal the SPEC CHECKSUMS entry recorded in Podfile.lock.
	ErrChecksumMismatch = errors.New("netsec: podspec SHA1 does not match Podfile.lock checksum")

	// ErrChecksumMissing is returned when Podfile.lock has no SPEC CHECKSUMS
	// entry for the pod.
	ErrChecksumMissing = errors.New("netsec: pod has no SPEC CHECKSUMS entry in Podfile.lock")

	// ErrSpecRepoMismatch is returned when the pod is served from a spec
	// repository other than the one recorded under SPEC REPOS in
	// Podfile.lock, including same-name pods published in another source.
	ErrSpecRepoMismatch = errors.New("netsec: pod source repo does not match Podfile.lock SPEC REPOS")

	// ErrHTTPSource is returned when a podspec uses an :http source, which
	// serves unversioned archives that cannot be pinned to a commit.
	ErrHTTPSource = errors.New("netsec: http podspec sources are not allowed")

	// ErrGitCommitNotPinned is returned when a :git source is missing a
	// :commit or the commit is not a 40-character SHA-1 hex digest.
	ErrGitCommitNotPinned = errors.New("netsec: git source must be pinned to a 40-character commit")

	// ErrUnsupportedSource is returned when a podspec source uses a type
	// the verifier does not allow.
	ErrUnsupportedSource = errors.New("netsec: unsupported podspec source")
)
