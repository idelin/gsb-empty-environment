// Package netsec provides strict MLLP framing and HL7 field validation for
// message gateways.
//
// Reader enforces block boundaries independently of input chunking. Writer
// rejects control bytes and misplaced segment separators before emitting a
// frame. DecodeField rejects HL7 escapes that restore MLLP control bytes.
package netsec
