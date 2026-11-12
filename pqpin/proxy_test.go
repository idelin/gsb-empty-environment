package pqpin

import "testing"

// stripHybridGroups rewrites a TLS record containing a ClientHello so that no
// hybrid group remains. Both the supported_groups extension (the advertised
// groups) and the key_share extension (the actual key share entries) must be
// edited together; leaving a hybrid key share in place while shrinking the
// group list desynchronizes the server parser and yields a record MAC failure.
// All enclosing length fields are patched accordingly.
func stripHybridGroups(record []byte) ([]byte, bool) {
	if len(record) < 5+4 || record[0] != recordHandshake {
		return nil, false
	}
	body := record[5:]
	if body[0] != 1 { // ClientHello
		return nil, false
	}
	hsLen := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
	if len(body) < 4+hsLen {
		return nil, false
	}
	msg := body[4 : 4+hsLen]

	// Skip version(2), random(32), session_id<..>, cipher_suites<..>,
	// compression_methods<..>.
	pos := 2 + 32
	pos += 1 + int(msg[pos])
	csLen := int(msg[pos])<<8 | int(msg[pos+1])
	pos += 2 + csLen
	cmLen := int(msg[pos])
	pos += 1 + cmLen
	if pos+2 > len(msg) {
		return nil, false
	}
	extTotalStart := pos
	extTotal := int(msg[pos])<<8 | int(msg[pos+1])
	extStart := pos + 2
	if extStart+extTotal > len(msg) {
		return nil, false
	}

	type rawExt struct {
		typ  uint16
		data []byte
	}
	var exts []rawExt
	cursor := extStart
	extEnd := extStart + extTotal
	for cursor+4 <= extEnd {
		typ := uint16(msg[cursor])<<8 | uint16(msg[cursor+1])
		extLen := int(msg[cursor+2])<<8 | int(msg[cursor+3])
		dataStart := cursor + 4
		dataEnd := dataStart + extLen
		if dataEnd > extEnd {
			return nil, false
		}
		exts = append(exts, rawExt{typ: typ, data: append([]byte{}, msg[dataStart:dataEnd]...)})
		cursor = dataEnd
	}
	if cursor != extEnd {
		return nil, false
	}

	changed := false
	var dropTypes []uint16
	for i := range exts {
		data := exts[i].data
		switch exts[i].typ {
		case 10: // supported_groups
			if len(data) < 2 {
				return nil, false
			}
			listLen := int(data[0])<<8 | int(data[1])
			if 2+listLen != len(data) || listLen%2 != 0 {
				return nil, false
			}
			var kept []byte
			for j := 0; j < listLen; j += 2 {
				group := uint16(data[2+j])<<8 | uint16(data[3+j])
				if !IsHybridGroup(group) {
					kept = append(kept, data[2+j], data[3+j])
				}
			}
			if len(kept) == 0 {
				return nil, false
			}
			newData := appendUint16(nil, uint16(len(kept)))
			newData = append(newData, kept...)
			if len(newData) != len(data) {
				exts[i].data = newData
				changed = true
			}
		case 51: // key_share (client): list of KeyShareEntry
			if len(data) < 2 {
				return nil, false
			}
			listLen := int(data[0])<<8 | int(data[1])
			if 2+listLen != len(data) {
				return nil, false
			}
			var keptEntries []byte
			j := 0
			for j < listLen {
				if j+4 > listLen {
					return nil, false
				}
				group := uint16(data[2+j])<<8 | uint16(data[3+j])
				shareLen := int(data[4+j])<<8 | int(data[5+j])
				entryEnd := j + 4 + shareLen
				if entryEnd > listLen {
					return nil, false
				}
				if !IsHybridGroup(group) {
					keptEntries = append(keptEntries, data[2+j:2+entryEnd]...)
				}
				j = entryEnd
			}
			if len(keptEntries) == 0 {
				return nil, false
			}
			newList := appendUint16(nil, uint16(len(keptEntries)))
			newList = append(newList, keptEntries...)
			// Go only sends a key share for its first-preference group. When
			// the attacker removes that (hybrid) entry the extension carries
			// no usable shares; a real on-path attacker drops the whole
			// extension so the server replies with an HRR for a classic group.
			if len(keptEntries) == 0 {
				dropTypes = append(dropTypes, 51)
			} else if 2+len(keptEntries) != len(data) {
				exts[i].data = newList
				changed = true
			}
		}
	}
	if !changed {
		if len(dropTypes) == 0 {
			return record, true
		}
	}

	// Rebuild the extensions block and the message around it.
	var newExts []byte
	for _, e := range exts {
		dropped := false
		for _, dt := range dropTypes {
			if e.typ == dt {
				dropped = true
			}
		}
		if dropped {
			continue
		}
		newExts = appendUint16(newExts, e.typ)
		newExts = appendUint16(newExts, uint16(len(e.data)))
		newExts = append(newExts, e.data...)
	}
	newMsg := append([]byte{}, msg[:extTotalStart]...)
	newMsg = appendUint16(newMsg, uint16(len(newExts)))
	newMsg = append(newMsg, newExts...)

	newHsLen := len(newMsg)
	msgWithHeader := append([]byte{body[0],
		byte(newHsLen >> 16), byte(newHsLen >> 8), byte(newHsLen)}, newMsg...)
	recordLen := 4 + newHsLen
	out := []byte{recordHandshake, record[1], record[2],
		byte(recordLen >> 8), byte(recordLen)}
	return append(out, msgWithHeader...), true
}

func putUint16(b []byte, v int) {
	b[0] = byte(v >> 8)
	b[1] = byte(v)
}

func TestStripHybridGroupsRewritesLengths(t *testing.T) {
	// Minimal ClientHello: TLS 1.2 legacy version, random, empty session id,
	// one cipher suite, null compression, supported_groups containing hybrid
	// and classic groups.
	groups := []uint16{hybrid, classic, uint16(0x0017)}
	var groupList []byte
	groupList = appendUint16(groupList, uint16(len(groups)*2))
	for _, g := range groups {
		groupList = appendUint16(groupList, g)
	}
	var ext []byte
	ext = appendUint16(ext, 10) // supported_groups
	ext = appendUint16(ext, uint16(len(groupList)))
	ext = append(ext, groupList...)

	var msg []byte
	msg = append(msg, 0x03, 0x03)
	msg = append(msg, make([]byte, 32)...)
	msg = append(msg, 0)                      // session id len
	msg = appendUint16(msg, 2)                // cipher suites length
	msg = append(msg, 0x13, 0x01)             // one suite
	msg = append(msg, 1, 0)                   // compression methods
	msg = appendUint16(msg, uint16(len(ext))) // extensions length
	msg = append(msg, ext...)

	record := []byte{recordHandshake, 0x03, 0x01}
	record = appendUint16(record, uint16(4+len(msg)))
	record = append(record, 1) // ClientHello
	record = append(record, byte(len(msg)>>16), byte(len(msg)>>8), byte(len(msg)))
	record = append(record, msg...)

	out, ok := stripHybridGroups(record)
	if !ok {
		t.Fatalf("strip failed")
	}
	if len(out) >= len(record) {
		t.Fatalf("rewritten record must be shorter")
	}

	// Verify patched outer lengths are internally consistent.
	recordLen := int(out[3])<<8 | int(out[4])
	if recordLen != len(out)-5 {
		t.Fatalf("record length mismatch: %d vs %d", recordLen, len(out)-5)
	}
	innerLen := int(out[6])<<16 | int(out[7])<<8 | int(out[8])
	if innerLen != recordLen-4 {
		t.Fatalf("handshake length mismatch")
	}

	// No hybrid group id (0x11EC) may remain anywhere in the output.
	for i := 0; i+1 < len(out); i++ {
		if out[i] == 0x11 && out[i+1] == 0xEC {
			t.Fatalf("hybrid group still present after stripping")
		}
	}
}
