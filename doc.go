// Package netsec implements safe application of compressed firmware upgrade
// packages whose declared destination ranges could otherwise overlap each
// other or clobber protected boot data.
//
// The two-phase design keeps the target device out of the decompression path:
//
//   - Prepare validates every destination range, every protected range and
//     the aggregate expansion budget, then decompresses all partitions into a
//     private staging directory and rejects the whole package if the actual
//     length or digest of any partition does not match the manifest.
//   - Commit re-verifies the staged bytes and only then writes them to the
//     device at the declared offsets.
//
// Decompression callbacks never receive the target device: they write only to
// isolated staging files, so a faulty partition or a corrupt final item leaves
// the device with zero partial writes.
package netsec
