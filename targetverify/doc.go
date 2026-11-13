// Package targetverify cross-checks Director and Image (Oem) targets
// metadata before an automotive ECU installs a firmware update.
//
// The package assumes the Uptane trust split: the Director repository tells
// a specific ECU which image it should install, while the Image repository
// describes the available images. Compromising only one side must never be
// enough to make an ECU install malicious firmware. A target is therefore
// installable only when all of the following hold:
//
//   - Both metadata documents carry at least their configured threshold of
//     valid signatures from distinct, locally trusted keys.
//   - Neither document is expired at verification time.
//   - Neither document has a version lower than the highest version already
//     seen for that repository (rollback protection).
//   - A Director target names the ECU serial number and hardware identifier
//     of this ECU, and exactly one Director target does so.
//   - A target with the same file name exists in the Image repository and
//     its length and SHA-256 digest are identical on both sides.
//   - Targets that appear on only one side are reported but never installed.
//
// After downloading the resulting artifact, call VerifiedTarget.VerifyArtifact
// to confirm its length and SHA-256 before installation.
package targetverify
