// Package policyauth implements the pre-unseal policy-authorization check
// for sealed keys on a measured-boot device.
//
// The flow mirrors TPM2_PolicyAuthorize: an authorization authority signs an
// approved PCR policy digest together with a policyRef that pins the device
// and the sealed object. Before a sealed key is released, the boot service
// verifies the signature, pins the signer to the public-key name fixed in the
// object policy, checks the device/object binding, and rejects any token whose
// monotonic generation is below the persisted NV counter. The counter is
// advanced only after the release/unseal callback succeeds, so a failed
// release never consumes an authorization generation.
package policyauth
