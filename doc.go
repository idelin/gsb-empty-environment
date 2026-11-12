// Package netsec provides acceptance-side verification of binary signature
// coverage.
//
// Signature formats may exclude arbitrary intervals of a file while the
// loader still maps and executes those, or loader-synthesized, bytes.
// VerifyCoverage combines the signature's covered file intervals, the
// loader's file-to-memory mappings and the executable virtual-address
// ranges to enumerate every executable byte that is not authenticated:
//
//   - unsigned-file: executable file-backed bytes whose file offset lies
//     outside all signed coverage intervals;
//   - zero-fill: executable bytes produced by loader zero-fill beyond a
//     mapping's file-backed content (for example .bss), which can never be
//     signature-covered;
//   - unbacked: executable addresses with no source mapping at all.
//
// Mappings may overlap or alias the same file region. Each mapping is an
// independent executable source, so any source whose bytes fail
// authentication is reported separately rather than being hidden by another
// mapping that happens to be signed.
package netsec
