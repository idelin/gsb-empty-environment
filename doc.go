// Package netsec provides strict HPACK header-block decoding for protocol
// gateways.
//
// Dynamic table size updates are accepted only before the first header field
// representation. Multiple consecutive updates are allowed, but each update
// must be within the SETTINGS_HEADER_TABLE_SIZE limit configured on the
// Decoder. Indexed references past the live static and dynamic tables are
// rejected.
package netsec
