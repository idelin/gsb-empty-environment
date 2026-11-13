package netsec

import (
	"fmt"
)

// IXFRStatus describes what the caller should do with an IXFR response.
type IXFRStatus uint8

const (
	// IXFRDiscard means the response must be dropped without applying it
	// and without attempting AXFR. This is used when the response serial is
	// older than the local serial, or when the RFC 1982 comparison is
	// undefined (the serials differ by exactly 2^31).
	IXFRDiscard IXFRStatus = iota
	// IXFRUpToDate means the response serial equals the local serial; no
	// transfer is needed.
	IXFRUpToDate
	// IXFRApply means every delta was structurally validated and may be
	// applied.
	IXFRApply
	// IXFRFallbackAXFR means the response is malformed, non-monotonic, or
	// has a broken chain. The whole response must be discarded and a full
	// AXFR requested instead.
	IXFRFallbackAXFR
)

func (s IXFRStatus) String() string {
	switch s {
	case IXFRDiscard:
		return "discard"
	case IXFRUpToDate:
		return "up-to-date"
	case IXFRApply:
		return "apply"
	case IXFRFallbackAXFR:
		return "fallback-axfr"
	default:
		return fmt.Sprintf("IXFRStatus(%d)", uint8(s))
	}
}

// Delta is one IXFR change chunk. OldSOA and NewSOA are the structural SOA
// boundaries; Deleted and Added contain the non-SOA records for the chunk.
type Delta struct {
	OldSOA  Record
	NewSOA  Record
	Deleted []Record
	Added   []Record
}

// IXFRDecision is the result of IXFR admission. Err is non-nil when the
// response was discarded or rejected in favour of AXFR.
type IXFRDecision struct {
	Status IXFRStatus
	Head   Record
	Deltas []Delta
	Err    error
}

// ShouldApply reports whether the caller may apply Deltas.
func (d IXFRDecision) ShouldApply() bool {
	return d.Status == IXFRApply
}

// RequiresAXFR reports whether the caller must fall back to a full AXFR.
func (d IXFRDecision) RequiresAXFR() bool {
	return d.Status == IXFRFallbackAXFR
}

// Reason returns the rejection reason, or an empty string for an accepted
// response.
func (d IXFRDecision) Reason() string {
	if d.Err == nil {
		return ""
	}
	return d.Err.Error()
}

// AdmitIXFR validates a complete IXFR response against the local SOA serial.
// It never applies records. A response is accepted only when every change
// chunk forms one continuous serial chain from the local version to the
// response's first SOA, and the final SOA is identical to the first SOA.
func AdmitIXFR(localSerial uint32, response []Record) IXFRDecision {
	if len(response) == 0 {
		return ixfrFallback(Record{}, "empty IXFR response")
	}

	head := response[0]
	if !head.IsSOA() {
		return ixfrFallback(head, "IXFR response does not start with a SOA record")
	}

	switch CompareSerial(head.SOA.Serial, localSerial) {
	case SerialEqual:
		return IXFRDecision{
			Status: IXFRUpToDate,
			Head:   head.Clone(),
		}
	case SerialLess:
		return ixfrDiscard(
			head,
			"response serial %d is older than local serial %d",
			head.SOA.Serial, localSerial,
		)
	case SerialUndefined:
		return ixfrDiscard(
			head,
			"response serial %d differs from local serial %d by exactly 2^31; RFC 1982 comparison is undefined",
			head.SOA.Serial, localSerial,
		)
	}

	if len(response) < 3 {
		return ixfrFallback(head, "IXFR response with a newer serial has no change data")
	}

	lastSOAIndex := -1
	for i := len(response) - 1; i >= 0; i-- {
		if response[i].IsSOA() {
			lastSOAIndex = i
			break
		}
	}
	if lastSOAIndex < 0 {
		return ixfrFallback(head, "IXFR response has no final SOA")
	}
	if !response[lastSOAIndex].Equal(head) {
		return ixfrFallback(head, "final SOA is not identical to the first SOA")
	}

	if !response[1].IsSOA() {
		return ixfrFallback(head, "first change chunk does not start with a SOA")
	}
	if response[1].SOA.Serial != localSerial {
		return ixfrFallback(
			head,
			"first change chunk starts at serial %d, local serial is %d",
			response[1].SOA.Serial, localSerial,
		)
	}

	var deltas []Delta
	expectedOldSerial := localSerial
	index := 1

	for {
		oldSOA := response[index]
		if !oldSOA.IsSOA() {
			return ixfrFallback(head, "change chunk at record %d does not start with a SOA", index)
		}
		if oldSOA.SOA.Serial != expectedOldSerial {
			return ixfrFallback(
				head,
				"broken chain: chunk starts at serial %d, previous chunk ended at serial %d",
				oldSOA.SOA.Serial, expectedOldSerial,
			)
		}

		newIndex := index + 1
		for newIndex < len(response) && !response[newIndex].IsSOA() {
			newIndex++
		}
		if newIndex >= len(response) {
			return ixfrFallback(head, "change chunk starting at serial %d has no newer SOA", oldSOA.SOA.Serial)
		}

		newSOA := response[newIndex]
		if CompareSerial(newSOA.SOA.Serial, oldSOA.SOA.Serial) != SerialGreater {
			return ixfrFallback(
				head,
				"change chunk serial %d is not newer than %d",
				newSOA.SOA.Serial, oldSOA.SOA.Serial,
			)
		}

		nextIndex := newIndex + 1
		for nextIndex < len(response) && !response[nextIndex].IsSOA() {
			nextIndex++
		}

		if nextIndex >= len(response) {
			// The final change chunk may use the current SOA as its newer
			// SOA and omit a separate trailing sentinel copy.
			if !newSOA.Equal(head) {
				return ixfrFallback(head, "final SOA is not identical to the first SOA")
			}
			deltas = append(deltas, makeDelta(oldSOA, newSOA, response[index+1:newIndex], response[newIndex+1:]))
			break
		}

		nextSOA := response[nextIndex]
		if nextSOA.Equal(head) {
			// The current SOA is either the explicit terminating sentinel
			// or the newer SOA of the last chunk. If it is the sentinel it
			// must be the last record in the response.
			if nextIndex != lastSOAIndex || nextIndex != len(response)-1 {
				return ixfrFallback(head, "current SOA appears before the end of the IXFR response")
			}
			if nextSOA.SOA.Serial != newSOA.SOA.Serial {
				return ixfrFallback(
					head,
					"broken chain: final SOA serial %d does not match last chunk serial %d",
					nextSOA.SOA.Serial, newSOA.SOA.Serial,
				)
			}
			deltas = append(deltas, makeDelta(oldSOA, newSOA, response[index+1:newIndex], response[newIndex+1:nextIndex]))
			break
		}

		if nextSOA.SOA.Serial != newSOA.SOA.Serial {
			return ixfrFallback(
				head,
				"broken chain: previous chunk ended at serial %d, next chunk starts at serial %d",
				newSOA.SOA.Serial, nextSOA.SOA.Serial,
			)
		}

		deltas = append(deltas, makeDelta(oldSOA, newSOA, response[index+1:newIndex], response[newIndex+1:nextIndex]))
		expectedOldSerial = newSOA.SOA.Serial
		index = nextIndex
	}

	if len(deltas) == 0 {
		return ixfrFallback(head, "IXFR response contains no change chunks")
	}

	return IXFRDecision{
		Status: IXFRApply,
		Head:   head.Clone(),
		Deltas: deltas,
	}
}

func makeDelta(oldSOA, newSOA Record, deleted, added []Record) Delta {
	return Delta{
		OldSOA:  oldSOA.Clone(),
		NewSOA:  newSOA.Clone(),
		Deleted: cloneRecords(deleted),
		Added:   cloneRecords(added),
	}
}

func ixfrFallback(head Record, format string, args ...any) IXFRDecision {
	return IXFRDecision{
		Status: IXFRFallbackAXFR,
		Head:   head.Clone(),
		Err:    fmt.Errorf(format, args...),
	}
}

func ixfrDiscard(head Record, format string, args ...any) IXFRDecision {
	return IXFRDecision{
		Status: IXFRDiscard,
		Head:   head.Clone(),
		Err:    fmt.Errorf(format, args...),
	}
}

// ApplyIXFR validates response and atomically computes a replacement record
// set from base. If validation or application fails, base is not modified
// and the returned record set is nil; the caller must either keep the old
// zone (discard/up-to-date) or start an AXFR fallback as directed by the
// decision.
//
// The returned record set places the accepted current SOA first.
func ApplyIXFR(base []Record, response []Record) ([]Record, IXFRDecision, error) {
	localSOA, err := findZoneSOA(base)
	if err != nil {
		decision := IXFRDecision{
			Status: IXFRFallbackAXFR,
			Err:    err,
		}
		return nil, decision, err
	}

	decision := AdmitIXFR(localSOA.SOA.Serial, response)
	switch decision.Status {
	case IXFRUpToDate:
		return cloneRecords(base), decision, nil
	case IXFRDiscard, IXFRFallbackAXFR:
		return nil, decision, decision.Err
	}

	content := make([]Record, 0, len(base)+len(response))
	for _, record := range base {
		if record.IsSOA() {
			continue
		}
		content = append(content, record.Clone())
	}

	for i, delta := range decision.Deltas {
		for _, deleted := range delta.Deleted {
			if deleted.IsSOA() {
				err = fmt.Errorf("IXFR delta %d deletes a SOA outside a chunk boundary", i+1)
				return nil, ixfrFallbackWithError(decision, err), err
			}
			pos := findRecord(content, deleted)
			if pos < 0 {
				err = fmt.Errorf("IXFR delta %d deletes a record that is not present: %s", i+1, deleted)
				return nil, ixfrFallbackWithError(decision, err), err
			}
			content = append(content[:pos], content[pos+1:]...)
		}

		for _, added := range delta.Added {
			if added.IsSOA() {
				err = fmt.Errorf("IXFR delta %d adds a SOA outside a chunk boundary", i+1)
				return nil, ixfrFallbackWithError(decision, err), err
			}
			if findRecord(content, added) >= 0 {
				continue
			}
			content = append(content, added.Clone())
		}
	}

	current := response[0].Clone()
	records := make([]Record, 1, len(content)+1)
	records[0] = current
	records = append(records, content...)
	return records, decision, nil
}

// ApplyIXFR validates response and atomically applies it to z. On any
// discard or fallback decision z.Records is left completely unchanged.
func (z *Zone) ApplyIXFR(response []Record) (IXFRDecision, error) {
	if z == nil {
		err := fmt.Errorf("nil zone")
		return IXFRDecision{Status: IXFRFallbackAXFR, Err: err}, err
	}

	records, decision, err := ApplyIXFR(z.Records, response)
	if decision.Status != IXFRApply || err != nil {
		return decision, err
	}

	z.Records = records
	if z.Name == "" && decision.Head.IsSOA() {
		z.Name = decision.Head.Name
	}
	return decision, nil
}

func ixfrFallbackWithError(decision IXFRDecision, err error) IXFRDecision {
	return IXFRDecision{
		Status: IXFRFallbackAXFR,
		Head:   decision.Head,
		Err:    err,
	}
}

func findZoneSOA(records []Record) (Record, error) {
	var found Record
	count := 0
	for _, record := range records {
		if record.IsSOA() {
			found = record
			count++
		}
	}
	switch count {
	case 0:
		return Record{}, fmt.Errorf("zone has no SOA record")
	case 1:
		return found, nil
	default:
		return Record{}, fmt.Errorf("zone has %d SOA records", count)
	}
}

func findRecord(records []Record, want Record) int {
	for i, record := range records {
		if record.Equal(want) {
			return i
		}
	}
	return -1
}
