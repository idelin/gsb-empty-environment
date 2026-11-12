// Package netsec validates CocoaPods podspecs served by a specs mirror
// against a trusted Podfile.lock.
//
// A Pod client fetches podspecs over a CDN. Without verification an attacker
// who can replace a podspec on the CDN can redirect its source to a malicious
// repository, or publish a same-name pod in another spec source. This package
// closes those gaps with four checks, all driven by the Podfile.lock rather
// than source ordering in the Podfile:
//
//  1. Path sharding: the podspec must live at
//     Specs/<md5(name)[0]>/<md5(name)[1]>/<md5(name)[2]>/<Name>/<Version>/<Name>.podspec.json.
//  2. Checksum: the SHA-1 of the exact podspec bytes must equal the entry
//     under SPEC CHECKSUMS, so any edit (including a changed source) fails.
//  3. Spec repository: the source actually used must be the one recorded for
//     the pod under SPEC REPOS; a same-name pod from another source is
//     rejected.
//  4. Source policy: :http sources are rejected, and :git sources must pin a
//     full 40-character commit instead of a moving tag or branch.
//
// Typical use:
//
//	lock, err := netsec.ParseLockfile(lockFile)
//	if err != nil { /* handle */ }
//	verifier := netsec.NewVerifier(lock)
//	spec, err := verifier.VerifyFS(fsys, repoKey, requestPath)
//	if err != nil {
//	    // reject the response; errors.Is matches the netsec sentinel errors.
//	}
//	_ = spec
package netsec
