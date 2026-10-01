// Package strdict implements a versioned shared-string dictionary for object
// batches, together with a streaming encoder/decoder that mixes dictionary
// references and inline strings in one stream.
//
// Serialization scope (self-defined, data only — decoding never executes
// host code):
//
//	stream   := frame*
//	frame    := tag payload
//	tag      := 0x00            ; end of stream (clean EOF marker)
//	          | 0x01 uvarint n, n bytes   ; inline string, n <= MaxInlineLen
//	          | 0x02 uvarint idx          ; reference into current dictionary
//	          | 0x03 uvarint version      ; switch dictionary version
//
// A decode session is pinned to one dictionary version at a time. Frames
// before an explicit switch frame (0x03) are resolved against the old
// version; frames after it against the new one. Reference indices are never
// carried across a switch: the same index may mean a different string in the
// new generation.
//
// Safety properties:
//   - declared lengths are validated against MaxInlineLen and the remaining
//     byte budget before any buffer is allocated;
//   - the byte budget covers dictionary bytes plus staging/output bytes, and
//     expansion beyond the budget is rejected mid-stream;
//   - unknown reference indices are an error, never the empty string;
//   - dictionaries are frozen before publish and are immutable afterwards.
package strdict
