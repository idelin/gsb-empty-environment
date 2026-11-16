// Package netsec implements a hardening-oriented MLLP (Minimal Lower Layer
// Protocol) framer for HL7 v2 messages carried over TCP.
//
// MLLP frames are delimited by a single start byte 0x0B (VT) and the two-byte
// trailer 0x1C 0x0D (FS CR):
//
//	VT  payload...  FS  CR
//
// MLLP itself provides no escaping, so a payload that contains framing bytes
// cannot be unambiguously framed. An attacker can embed FS CR (and a second VT)
// inside an HL7 field value to make a naive receiver split one message in two
// and treat the tail as a new message from another sender. This package closes
// that hole in layers:
//
//   - [Parser]/[FrameReader] treat any second VT inside a frame and any
//     non-VT byte outside a frame as a fatal protocol error ([ErrProtocol]);
//     the caller is expected to tear the connection down. A trailing FS not
//     immediately followed by CR invalidates the frame ([ErrBadTrailer]) and
//     the parser resynchronizes at the next legal VT. Frames whose payload
//     exceeds the caller-supplied limit are invalidated
//     ([ErrFrameTooLarge]) and bytes are skipped up to the next VT.
//   - [FrameWriter] refuses payloads containing VT or FS instead of silently
//     emitting a frame that a peer could mis-split, and enforces CR as a
//     segment terminator.
//   - [ValidateStoredField] / [UnescapeField] reject HL7 \X0B\ / \X1C\
//     escape sequences once decoded, so framing bytes cannot reach stored
//     fields through the HL7 escaping layer.
//
// The incremental [Parser.Feed] accepts arbitrarily chunked bytes (including
// chunk boundaries inside the two-byte trailer); the emitted event stream is
// identical to feeding the whole buffer at once.
package netsec
