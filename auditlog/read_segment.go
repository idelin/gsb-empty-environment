package auditlog

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

func parseSegment(path string, index uint64, part, isTerminal bool, keys Keys) (parsedSegment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return parsedSegment{}, err
	}

	name := filepath.Base(path)
	reader := &byteReader{data: data}
	segment := parsedSegment{
		status: SegmentStatus{Index: index, File: name, Sealed: !part},
	}

	prevDigest := genesisDigest[:]
	var firstSeq, lastSeq uint64

	for {
		frameStart := reader.offset
		header, headerErr := reader.read(recordHeaderLen)
		if headerErr == errCleanEOF {
			break
		}
		if headerErr != nil {
			if headerErr == errTorn {
				return finishTorn(segment, part, isTerminal, name, index,
					frameStart, int64(len(data)), "incomplete frame header")
			}
			return parsedSegment{}, tamper(TamperFrame, index, lastSeq, name,
				frameStart, headerErr.Error())
		}

		switch header[0] {
		case recordMagic:
			if header[1] != version1 {
				return parsedSegment{}, tamper(TamperFrame, index, lastSeq, name,
					frameStart, fmt.Sprintf("unsupported record version %d", header[1]))
			}
			payloadLen := binary.BigEndian.Uint32(header[11:15])
			if payloadLen > maxPayloadSize {
				return parsedSegment{}, tamper(TamperFrame, index, lastSeq, name,
					frameStart, fmt.Sprintf("payload length %d exceeds limit", payloadLen))
			}

			payload, payloadErr := reader.read(int(payloadLen))
			if payloadErr != nil {
				if payloadErr == errTorn || payloadErr == errCleanEOF {
					return finishTorn(segment, part, isTerminal, name, index,
						frameStart, int64(len(data)), "incomplete record payload")
				}
				return parsedSegment{}, tamper(TamperFrame, index, lastSeq, name,
					frameStart, payloadErr.Error())
			}

			frame := make([]byte, recordHeaderLen+int(payloadLen))
			copy(frame, header)
			copy(frame[recordHeaderLen:], payload)

			seq := binary.BigEndian.Uint64(header[3:11])
			storedPrev := header[15 : 15+digestLen]
			storedMAC := header[15+digestLen : 15+digestLen+hmacLen]

			mac := hmac.New(sha256.New, keys.HMAC)
			mac.Write(frame[3:15])
			mac.Write(storedPrev)
			mac.Write(payload)
			if !hmac.Equal(storedMAC, mac.Sum(nil)) {
				return parsedSegment{}, tamper(TamperHMAC, index, seq, name,
					frameStart, "complete record failed HMAC authentication")
			}

			if segment.status.Records == 0 {
				firstSeq = seq
				segment.firstPrevDigest = append(segment.firstPrevDigest, storedPrev...)
			} else {
				if seq != lastSeq+1 {
					return parsedSegment{}, tamper(TamperChain, index, seq, name,
						frameStart, fmt.Sprintf("expected sequence %d", lastSeq+1))
				}
				if !hmac.Equal(storedPrev, prevDigest) {
					return parsedSegment{}, tamper(TamperChain, index, seq, name,
						frameStart, "predecessor digest mismatch")
				}
			}

			segment.records = append(segment.records, Record{
				Segment:  index,
				Sequence: seq,
				Payload:  append([]byte(nil), payload...),
			})
			segment.status.Records++
			lastSeq = seq
			prevDigest = recordDigest(frame)
			segment.lastDigest = append([]byte(nil), prevDigest...)

		case sealMagic:
			if header[1] != version1 {
				return parsedSegment{}, tamper(TamperFrame, index, lastSeq, name,
					frameStart, fmt.Sprintf("unsupported seal version %d", header[1]))
			}
			// The loop always reads a record-sized header; a seal header is
			// shorter, so rewind the over-read bytes before consuming the rest.
			overRead := int64(recordHeaderLen - sealHeaderLen)
			reader.offset -= overRead
			rest, restErr := reader.read(sealFrameLen - sealHeaderLen)
			if restErr != nil {
				if restErr == errTorn || restErr == errCleanEOF {
					return finishTorn(segment, part, isTerminal, name, index,
						frameStart, int64(len(data)), "incomplete seal")
				}
				return parsedSegment{}, tamper(TamperFrame, index, lastSeq, name,
					frameStart, restErr.Error())
			}

			sealFrame := make([]byte, sealFrameLen)
			copy(sealFrame, header[:sealHeaderLen])
			copy(sealFrame[sealHeaderLen:], rest)

			if err := verifySeal(sealFrame, index, firstSeq, lastSeq,
				segment.status.Records, segment.lastDigest, name, frameStart, keys); err != nil {
				return parsedSegment{}, err
			}

			segment.sealPrevDigest = append(segment.sealPrevDigest,
				sealFrame[35:35+digestLen]...)
			segment.sealDigest = append(segment.sealDigest, sealDigest(sealFrame)...)
			segment.status.SealDigest = append([]byte(nil), segment.sealDigest...)

			if _, trailing := reader.read(1); trailing != errCleanEOF {
				return parsedSegment{}, tamper(TamperSeal, index, lastSeq, name,
					reader.offset, "data appended after the segment seal")
			}

			segment.status.Sealed = true
			if segment.status.Records > 0 {
				segment.status.FirstSequence = firstSeq
				segment.status.LastSequence = lastSeq
			}
			segment.status.LastRecordDigest = append([]byte(nil), segment.lastDigest...)
			return segment, nil

		default:
			return parsedSegment{}, tamper(TamperFrame, index, lastSeq, name,
				frameStart, fmt.Sprintf("unknown frame magic 0x%02x", header[0]))
		}
	}

	if !part {
		return parsedSegment{}, tamper(TamperSeal, index, lastSeq, name,
			reader.offset, "sealed segment is missing its seal")
	}
	if segment.status.Records > 0 {
		segment.status.FirstSequence = firstSeq
		segment.status.LastSequence = lastSeq
		segment.status.LastRecordDigest = append([]byte(nil), segment.lastDigest...)
	}
	return segment, nil
}

func finishTorn(segment parsedSegment, part, isTerminal bool, name string,
	index uint64, frameStart, fileSize int64, detail string) (parsedSegment, error) {
	if !part {
		return parsedSegment{}, tamper(TamperFrame, index, 0, name, frameStart,
			detail+": incomplete frame outside the physical tail of an unsealed segment")
	}
	torn := TornTail{
		Segment: index,
		File:    name,
		Bytes:   fileSize - frameStart,
	}
	segment.torn = &torn
	segment.status.TornTailBytes = torn.Bytes
	if segment.status.Records > 0 {
		segment.status.FirstSequence = segment.records[0].Sequence
		segment.status.LastSequence = segment.records[len(segment.records)-1].Sequence
		segment.status.LastRecordDigest = append([]byte(nil), segment.lastDigest...)
	}
	// A non-terminal torn .part is power-loss residue that a recovery run
	// already superseded. It is retained as evidence, never overwritten, and
	// classified by the chain verifier rather than rejected here.
	return segment, nil
}

func verifySeal(sealFrame []byte, index, firstSeq, lastSeq uint64, recordCount int,
	lastDigest []byte, name string, offset int64, keys Keys) error {
	sealIndex := binary.BigEndian.Uint64(sealFrame[3:11])
	count := binary.BigEndian.Uint64(sealFrame[11:19])
	sealFirst := binary.BigEndian.Uint64(sealFrame[19:27])
	sealLast := binary.BigEndian.Uint64(sealFrame[27:35])
	lastRecord := sealFrame[35+digestLen : 35+2*digestLen]
	sig := sealFrame[sealHeaderLen:]

	if sealIndex != index {
		return tamper(TamperSeal, index, lastSeq, name, offset,
			fmt.Sprintf("seal index %d does not match file index", sealIndex))
	}
	if !ed25519.Verify(keys.Verify, sealFrame[:sealHeaderLen], sig) {
		return tamper(TamperSeal, index, lastSeq, name, offset,
			"seal signature verification failed")
	}
	if count != uint64(recordCount) {
		return tamper(TamperSeal, index, lastSeq, name, offset,
			"seal record count does not match segment contents")
	}
	if count == 0 {
		if sealFirst != 0 || sealLast != 0 {
			return tamper(TamperSeal, index, lastSeq, name, offset,
				"empty segment seal must carry zero sequence numbers")
		}
	} else if sealFirst != firstSeq || sealLast != lastSeq {
		return tamper(TamperSeal, index, lastSeq, name, offset,
			"seal sequence range does not match segment contents")
	}

	expectedLastRecord := genesisDigest[:]
	if recordCount > 0 {
		expectedLastRecord = lastDigest
	}
	if !hmac.Equal(lastRecord, expectedLastRecord) {
		return tamper(TamperSeal, index, lastSeq, name, offset,
			"seal last-record digest does not match segment contents")
	}
	return nil
}
