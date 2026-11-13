// Package netsec implements root-signed firmware key manifests and
// threshold-signed firmware admission for a boot host.
//
// A manifest's canonical body is a deterministic binary encoding of the device
// model, generation, validity window, firmware threshold, key entries, and
// revoked key IDs. Root signatures cover that complete body. Firmware
// signatures cover a separate canonical statement containing the model, image
// digest, firmware version, and manifest generation.
//
// Host applies monotonic-generation, revocation, key-material, and threshold
// policy before atomically committing accepted state through a TrustedStore.
package netsec
