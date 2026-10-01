// Package streamdec decodes a small, self-delimiting, length-prefixed data
// format. Supported serialization scope (data only, no code):
//
//	value  := object | string | int | bool | null
//	object := '{' field* '}'
//	field  := string value          // the string is the field name
//	string := 'S' <decimal length> ':' <length bytes of UTF-8>
//	int    := 'I' ['-'] <decimal digits> ';'
//	bool   := 'T' | 'F'
//	null   := 'N'
//
// Example: {"name":"Alice","age":30,"ok":true} is encoded as
//
//	{S4:nameS5:AliceS3:ageI30;S2:okT}
//
// A stream is a sequence of top-level values. The decoder accepts input in
// arbitrarily small chunks (a length header may itself be split across
// chunks) and publishes the events of a top-level object only once the
// object is complete. Lengths are validated against MaxStringLen before any
// buffer is allocated.
package streamdec
