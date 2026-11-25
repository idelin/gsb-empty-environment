package replayhandoff

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// MarshalJSON encodes an interval as [lo,hi).
func (iv Interval) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]uint64{uint64(iv.Lo), uint64(iv.Hi)})
}

// UnmarshalJSON decodes an interval and validates its endpoints.
func (iv *Interval) UnmarshalJSON(data []byte) error {
	if iv == nil {
		return errors.New("replayhandoff: nil interval receiver")
	}
	var values [2]uint64
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	decoded, err := NewInterval(Seq(values[0]), Seq(values[1]))
	if err != nil {
		return err
	}
	*iv = decoded
	return nil
}

// MarshalJSON encodes a canonical interval array.
func (set IntervalSet) MarshalJSON() ([]byte, error) {
	return json.Marshal(set.Intervals())
}

// UnmarshalJSON normalizes and validates the interval array.
func (set *IntervalSet) UnmarshalJSON(data []byte) error {
	if set == nil {
		return errors.New("replayhandoff: nil interval-set receiver")
	}
	if string(data) == "null" {
		*set = IntervalSet{}
		return nil
	}
	var intervals []Interval
	if err := json.Unmarshal(data, &intervals); err != nil {
		return err
	}
	decoded, err := NewIntervalSet(intervals...)
	if err != nil {
		return err
	}
	*set = decoded
	return nil
}

// MarshalJSON encodes a root as lowercase hexadecimal.
func (r Root) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.String())
}

// UnmarshalJSON decodes a 32-byte root from hexadecimal.
func (r *Root) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errors.New("replayhandoff: nil root receiver")
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	bytes, err := hex.DecodeString(text)
	if err != nil {
		return err
	}
	decoded, err := RootFromBytes(bytes)
	if err != nil {
		return err
	}
	*r = decoded
	return nil
}

type partitionNodesJSON struct {
	Partition Partition
	Nodes     []NodeID
}

type partitionSeenJSON struct {
	Partition Partition
	Seen      IntervalSet
}

type certificateInputJSON struct {
	Nodes            []NodeID
	NodesByPartition []partitionNodesJSON
	NewNode          NodeID
	Leases           []Lease
	LocalSeen        []partitionSeenJSON
	KnownProofs      []RangeProof
	CandidatePages   []ProofPage
	Queries          []QuerySegment
}

// MarshalJSON avoids Go's unsupported struct-keyed maps and emits a stable,
// transport-friendly representation.
func (input CertificateInput) MarshalJSON() ([]byte, error) {
	dto := certificateInputJSON{
		Nodes:       append([]NodeID(nil), input.Nodes...),
		NewNode:     input.NewNode,
		Leases:      append([]Lease(nil), input.Leases...),
		KnownProofs: append([]RangeProof(nil), input.KnownProofs...),
		Queries:     append([]QuerySegment(nil), input.Queries...),
	}
	dto.CandidatePages = make([]ProofPage, 0, len(input.CandidatePages))
	for _, page := range input.CandidatePages {
		dto.CandidatePages = append(dto.CandidatePages, clonePage(page))
	}
	keys := make([]Partition, 0, len(input.NodesByPartition))
	for partition := range input.NodesByPartition {
		keys = append(keys, partition)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, partition := range keys {
		dto.NodesByPartition = append(dto.NodesByPartition, partitionNodesJSON{
			Partition: partition,
			Nodes:     append([]NodeID(nil), input.NodesByPartition[partition]...),
		})
	}
	keys = keys[:0]
	for partition := range input.LocalSeen {
		keys = append(keys, partition)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, partition := range keys {
		dto.LocalSeen = append(dto.LocalSeen, partitionSeenJSON{
			Partition: partition,
			Seen:      input.LocalSeen[partition],
		})
	}
	return json.Marshal(dto)
}

// UnmarshalJSON restores the self-contained certificate input.
func (input *CertificateInput) UnmarshalJSON(data []byte) error {
	if input == nil {
		return errors.New("replayhandoff: nil certificate-input receiver")
	}
	var dto certificateInputJSON
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}
	decoded := CertificateInput{
		Nodes:          append([]NodeID(nil), dto.Nodes...),
		NewNode:        dto.NewNode,
		Leases:         append([]Lease(nil), dto.Leases...),
		KnownProofs:    append([]RangeProof(nil), dto.KnownProofs...),
		CandidatePages: make([]ProofPage, 0, len(dto.CandidatePages)),
		Queries:        append([]QuerySegment(nil), dto.Queries...),
	}
	if len(dto.NodesByPartition) > 0 {
		decoded.NodesByPartition = make(map[Partition][]NodeID, len(dto.NodesByPartition))
		for _, item := range dto.NodesByPartition {
			if err := item.Partition.Validate(); err != nil {
				return fmt.Errorf("replayhandoff: routing partition: %w", err)
			}
			decoded.NodesByPartition[item.Partition] = append([]NodeID(nil), item.Nodes...)
		}
	}
	if len(dto.LocalSeen) > 0 {
		decoded.LocalSeen = make(map[Partition]IntervalSet, len(dto.LocalSeen))
		for _, item := range dto.LocalSeen {
			if err := item.Partition.Validate(); err != nil {
				return fmt.Errorf("replayhandoff: local-seen partition: %w", err)
			}
			if err := ValidateSet(item.Seen); err != nil {
				return err
			}
			decoded.LocalSeen[item.Partition] = item.Seen
		}
	}
	for _, page := range dto.CandidatePages {
		decoded.CandidatePages = append(decoded.CandidatePages, clonePage(page))
	}
	*input = decoded
	return nil
}
