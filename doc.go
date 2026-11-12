// Package netsec verifies OCI container images against their in-toto
// attestations at Kubernetes admission time.
//
// It closes two common supply-chain gaps:
//
//   - An attestation replayed from another image, or one whose subject names
//     a layer/config digest instead of the deployed manifest digest, is
//     rejected because VerifyManifestSubject requires the exact sha256
//     digest of the raw manifest bytes to appear in the in-toto subject set.
//   - A tampered image is rejected because VerifyLayers checks each
//     compressed layer blob against its manifest descriptor digest, and
//     checks the sha256 of the decompressed content against the
//     corresponding rootfs.diff_ids entry, one-to-one and in order.
//
// All failures wrap an exported sentinel error (ErrSubjectMismatch,
// ErrLayerDigest, ErrDiffID, ErrDecompress and so on) so admission policies
// can map them to denial reasons via errors.Is. The package depends only on
// the Go standard library.
package netsec
