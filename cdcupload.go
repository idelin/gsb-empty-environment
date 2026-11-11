// Package cdcupload implements a resumable content-defined-chunking upload
// component for collectors that repeatedly upload slightly modified versions
// of large files.
//
// Chunk boundaries are derived from the data itself with a Gear rolling hash
// (FastCDC style normalized chunking), so inserting or deleting bytes only
// changes the chunks near the edit. Chunks are addressed by their SHA-256
// digest and deduplicated both against the local session state and the remote
// store. An upload can be interrupted and resumed any number of times; the
// manifest is published last, atomically.
package cdcupload
