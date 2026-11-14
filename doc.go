// Package netsec implements a transactional HTTP/2 header-block decoding and
// tenant-isolation layer for an embedded gateway.
//
// A Decoder buffers HEADERS and CONTINUATION fragments until END_HEADERS,
// decodes the complete HPACK block against a private clone of the dynamic
// table, and validates the resulting field list. Dynamic table changes are
// published only after the whole block has decoded successfully, pseudo-header
// ordering and duplication rules pass, field names are lowercase, and
// sensitive headers have not been offered for dynamic indexing. Compression
// errors, cancellation, budget violations and connection errors therefore
// leave the previous table untouched.
//
// SecurityContext binds a compression context to one peer connection and one
// tenant. Switching tenants on the same peer is rejected until a synchronized
// SETTINGS record for the target peer/tenant/generation is available, at which
// point the dynamic table is reset. CONTINUATION frames that arrive after a
// block was canceled are consumed for frame alignment only and can never
// attach to a newly opened stream.
package netsec
