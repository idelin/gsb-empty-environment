// Package netsec parses untrusted tensor containers.
//
// A container consists of an eight-byte little-endian metadata length, one
// strict UTF-8 JSON object, and a binary data region. Parse validates the
// complete metadata and every tensor range before returning read-only
// io.ReaderAt views. Open additionally binds those views to one open file
// object, so path replacement, truncation, modification, or Close makes later
// reads fail rather than silently reading different bytes.
package netsec
