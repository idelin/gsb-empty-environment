// Package replicaread implements quorum reads over versioned replicas
// with read-repair. Each replica answers with a versioned value or
// tombstone; once a configurable number of responses (the quorum)
// arrives, the highest version wins. Late responses carrying a higher
// version open a new result generation instead of mutating the
// already-published one.
package replicaread

import "errors"

// ErrQuorumFailed is reported when the quorum was reached but every
// response was an error, so no version could be selected.
var ErrQuorumFailed = errors.New("replicaread: quorum reached without any successful response")

// Response is a single replica's answer to a read.
type Response struct {
	Replica string
	Version Version
	Payload Payload
	// Err, when non-nil, marks a failed answer. Failed answers count
	// toward the quorum but never toward version selection.
	Err error
}

// Result is the immutable outcome of one completed generation.
type Result struct {
	// Generation identifies which result generation produced this
	// result, starting at 1.
	Generation uint64
	// Version is the highest version observed among successful answers.
	Version Version
	// Payload is the content at Version. Meaningless when Conflict or
	// Err is set.
	Payload Payload
	// Conflict is true when replicas disagree about the content stored
	// at the winning version.
	Conflict bool
	// Conflicts lists every distinct payload observed at the winning
	// version, in arrival order. Only set when Conflict is true.
	Conflicts []Payload
	// Responded lists the replicas whose answers formed this
	// generation, in arrival order.
	Responded []string
	// Repaired lists the confirmed-stale replicas a repair was sent
	// to, in repair order.
	Repaired []string
	// Err is ErrQuorumFailed when the quorum answered only failures.
	Err error
}

// RepairFunc delivers a read-repair write to a stale replica.
type RepairFunc func(replica string, v Version, p Payload)

// Reader collects replica responses for one logical read and produces
// result generations as they complete.
type Reader struct {
	quorum   int
	onRepair RepairFunc

	gen     *generation
	results []*Result
}

type generation struct {
	id        uint64
	responses []Response
	seen      map[string]bool
	done      bool
}

// NewReader creates a Reader that completes a generation after quorum
// successful-or-failed answers. onRepair may be nil.
func NewReader(quorum int, onRepair RepairFunc) *Reader {
	if quorum < 1 {
		quorum = 1
	}
	r := &Reader{quorum: quorum, onRepair: onRepair}
	r.gen = &generation{id: 1, seen: make(map[string]bool)}
	return r
}

// HandleResponse feeds one replica answer into the reader. Callers
// control the arrival order, which makes quorum and repair behaviour
// fully deterministic.
//
// Once the current generation has completed, an answer carrying a
// higher version than the published result opens a new generation;
// the old result is never modified. Stale or duplicate late answers
// are dropped.
func (r *Reader) HandleResponse(resp Response) {
	if r.gen.done {
		last := r.results[len(r.results)-1]
		if resp.Err != nil || resp.Version.Compare(last.Version) <= 0 {
			return
		}
		r.gen = &generation{id: last.Generation + 1, seen: make(map[string]bool)}
	}
	if r.gen.seen[resp.Replica] {
		return
	}
	r.gen.seen[resp.Replica] = true
	r.gen.responses = append(r.gen.responses, resp)
	if len(r.gen.responses) >= r.quorum {
		r.finalize()
	}
}

// Results returns every completed generation in order. Returned
// results are never mutated by later responses.
func (r *Reader) Results() []*Result {
	out := make([]*Result, len(r.results))
	copy(out, r.results)
	return out
}

// Pending reports whether the current generation is still waiting for
// answers.
func (r *Reader) Pending() bool { return !r.gen.done }

func (r *Reader) finalize() {
	gen := r.gen
	res := &Result{Generation: gen.id}

	var winners []Response
	for _, resp := range gen.responses {
		res.Responded = append(res.Responded, resp.Replica)
		if resp.Err != nil {
			continue
		}
		switch {
		case len(winners) == 0 || resp.Version.Compare(winners[0].Version) > 0:
			winners = []Response{resp}
		case resp.Version.Compare(winners[0].Version) == 0:
			winners = append(winners, resp)
		}
	}

	if len(winners) == 0 {
		res.Err = ErrQuorumFailed
	} else {
		res.Version = winners[0].Version
		res.Payload = winners[0].Payload
		distinct := []Payload{res.Payload}
		for _, w := range winners[1:] {
			if !containsPayload(distinct, w.Payload) {
				distinct = append(distinct, w.Payload)
			}
		}
		if len(distinct) > 1 {
			res.Conflict = true
			res.Conflicts = distinct
		}
	}

	// Repair only replicas that actually answered with an older
	// version; silent replicas are never touched.
	if !res.Conflict && res.Err == nil {
		for _, resp := range gen.responses {
			if resp.Err == nil && resp.Version.Less(res.Version) {
				res.Repaired = append(res.Repaired, resp.Replica)
				if r.onRepair != nil {
					r.onRepair(resp.Replica, res.Version, res.Payload)
				}
			}
		}
	}

	gen.done = true
	r.results = append(r.results, res)
}

func containsPayload(list []Payload, p Payload) bool {
	for _, item := range list {
		if item.Equal(p) {
			return true
		}
	}
	return false
}
