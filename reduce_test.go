package taskpkg_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"

	taskpkg "example.org/domainreissue/q001"
)

// hostTree models the attachment decoder input. The out-of-bounds read
// reproduces only for specific combinations of the relative payload
// reference offset and the allocation length: the decoder allocates
// attach.length bytes at site "attach.buffer" and reads the payload
// object at its absolute offset in the message.
func hostTree() *taskpkg.Node {
	return &taskpkg.Node{
		Tag:  0,
		Name: "message",
		Fields: []taskpkg.IntField{
			{Name: "version", Value: 2, Candidates: []uint64{2, 1}},
		},
		Children: []*taskpkg.Node{
			{
				Tag:      1,
				Name:     "meta",
				Optional: true,
				Fields: []taskpkg.IntField{
					{Name: "flags", Value: 7, Candidates: []uint64{7, 0, 3}},
				},
			},
			{
				Tag:  2,
				Name: "attach",
				Fields: []taskpkg.IntField{
					{Name: "length", Value: 8, Candidates: []uint64{8, 4, 2, 1, 0, 16, 32, 64}},
				},
				Refs: []taskpkg.Ref{{Name: "payloadPtr", Target: "payload"}},
			},
			{
				Tag:      3,
				Name:     "payload",
				Optional: true,
				Fields: []taskpkg.IntField{
					{Name: "pad", Value: 5, Candidates: []uint64{5, 0}},
				},
			},
			{Tag: 4, Name: "trailer", Optional: true},
		},
	}
}

type decodedNode struct {
	name     string
	fields   []uint64
	start    int
	children []*decodedNode
}

// hostSchema maps node tags to (name, int field count, ref count).
var hostSchema = map[uint64]struct {
	name   string
	fields int
	refs   int
}{
	0: {"message", 1, 0},
	1: {"meta", 1, 0},
	2: {"attach", 1, 1},
	3: {"payload", 1, 0},
	4: {"trailer", 0, 0},
}

func parseHost(data []byte) (*decodedNode, error) {
	pos := 0
	var rec func() (*decodedNode, error)
	rec = func() (*decodedNode, error) {
		start := pos
		tag, n := binary.Uvarint(data[pos:])
		if n <= 0 {
			return nil, errors.New("truncated tag")
		}
		pos += n
		sch, ok := hostSchema[tag]
		if !ok {
			return nil, fmt.Errorf("unknown tag %d", tag)
		}
		node := &decodedNode{name: sch.name, start: start}
		for i := 0; i < sch.fields; i++ {
			v, m := binary.Uvarint(data[pos:])
			if m <= 0 {
				return nil, errors.New("truncated field")
			}
			pos += m
			node.fields = append(node.fields, v)
		}
		for i := 0; i < sch.refs; i++ {
			if pos+4 > len(data) {
				return nil, errors.New("truncated ref")
			}
			pos += 4
		}
		nc, m := binary.Uvarint(data[pos:])
		if m <= 0 {
			return nil, errors.New("truncated child count")
		}
		pos += m
		for i := 0; i < int(nc); i++ {
			c, err := rec()
			if err != nil {
				return nil, err
			}
			node.children = append(node.children, c)
		}
		return node, nil
	}
	root, err := rec()
	if err != nil {
		return nil, err
	}
	if pos != len(data) {
		return nil, errors.New("trailing bytes")
	}
	return root, nil
}

func findDecoded(n *decodedNode, name string) *decodedNode {
	if n == nil {
		return nil
	}
	if n.name == name {
		return n
	}
	for _, c := range n.children {
		if f := findDecoded(c, name); f != nil {
			return f
		}
	}
	return nil
}

// hostValidate is the independent format validator.
func hostValidate(data []byte) error {
	root, err := parseHost(data)
	if err != nil {
		return err
	}
	if root.fields[0] == 0 || root.fields[0] > 2 {
		return errors.New("unsupported version")
	}
	attach := findDecoded(root, "attach")
	if attach == nil {
		return errors.New("missing attach")
	}
	if attach.fields[0] > 48 {
		return errors.New("attachment length exceeds 48")
	}
	return nil
}

// hostExecute is the deterministic execution callback. It reports the
// object actually read, the out-of-bounds offset, and the allocation
// source. A missing trailer is an ordinary parse failure, not the bug.
func hostExecute(data []byte) taskpkg.Evidence {
	root, err := parseHost(data)
	if err != nil {
		return taskpkg.Evidence{OK: false, Detail: err.Error()}
	}
	if findDecoded(root, "trailer") == nil {
		return taskpkg.Evidence{OK: false, Detail: "missing trailer"}
	}
	attach := findDecoded(root, "attach")
	payload := findDecoded(root, "payload")
	if attach == nil || payload == nil {
		return taskpkg.Evidence{OK: false, Detail: "missing attachment"}
	}
	allocLen := int64(attach.fields[0])
	readAt := int64(payload.start)
	ev := taskpkg.Evidence{
		OK:         true,
		ReadObject: "payload",
		OOBOffset:  readAt,
		AllocSite:  "attach.buffer",
	}
	if readAt >= allocLen {
		ev.OOB = true
		ev.Detail = fmt.Sprintf("read at %d with allocation %d", readAt, allocLen)
	}
	return ev
}

func TestReduceMatchesExhaustive(t *testing.T) {
	res, err := taskpkg.Reduce(hostTree(), hostValidate, hostExecute, taskpkg.Budget{})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !res.ProvenMinimal {
		t.Fatal("expected proven minimal result with unlimited budget")
	}
	if res.NodeCount != 4 {
		t.Fatalf("node count = %d, want 4", res.NodeCount)
	}
	if res.Bytes != 15 {
		t.Fatalf("encoded bytes = %d, want 15", res.Bytes)
	}
	if len(res.Dropped) != 1 || res.Dropped[0] != "meta" {
		t.Fatalf("dropped = %v, want [meta]", res.Dropped)
	}
	if !res.Evidence.OK || !res.Evidence.OOB {
		t.Fatalf("final evidence does not reproduce: %+v", res.Evidence)
	}
	if res.Evidence.AllocSite != res.Baseline.AllocSite {
		t.Fatalf("allocation site changed: %q vs baseline %q",
			res.Evidence.AllocSite, res.Baseline.AllocSite)
	}
	if res.Evidence.ReadObject != "payload" {
		t.Fatalf("read object = %q, want payload", res.Evidence.ReadObject)
	}

	ref, err := taskpkg.Exhaustive(hostTree(), hostValidate, hostExecute, 100000)
	if err != nil {
		t.Fatalf("Exhaustive: %v", err)
	}
	if err := taskpkg.CrossCheck(res, ref, hostExecute); err != nil {
		t.Fatalf("CrossCheck: %v", err)
	}
	if err := taskpkg.VerifyTrace(hostTree(), res, hostValidate, hostExecute); err != nil {
		t.Fatalf("VerifyTrace: %v", err)
	}
	if err := taskpkg.VerifyTrace(hostTree(), ref, hostValidate, hostExecute); err != nil {
		t.Fatalf("VerifyTrace(exhaustive): %v", err)
	}
}

func TestParseFailureIsNotAReproduction(t *testing.T) {
	res, err := taskpkg.Reduce(hostTree(), hostValidate, hostExecute, taskpkg.Budget{})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	sawParseFailure := false
	for _, step := range res.Trace {
		if !step.Executed {
			continue
		}
		if !step.Evidence.OK {
			sawParseFailure = true
		}
		if step.Reproduces && (!step.Evidence.OK || !step.Evidence.OOB) {
			t.Fatalf("step %d accepted without a reproduced OOB read: %+v", step.Seq, step.Evidence)
		}
	}
	if !sawParseFailure {
		t.Fatal("expected at least one ordinary parse failure among candidates")
	}
}

func TestValidationGatesCandidates(t *testing.T) {
	res, err := taskpkg.Reduce(hostTree(), hostValidate, hostExecute, taskpkg.Budget{})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	sawInvalid := false
	for _, step := range res.Trace {
		if !step.Valid && strings.Contains(step.ValidErr, "exceeds 48") {
			sawInvalid = true
		}
		if !step.Valid && step.Executed {
			t.Fatalf("step %d executed despite failing validation", step.Seq)
		}
	}
	if !sawInvalid {
		t.Fatal("expected the length=64 candidate to be rejected by validation")
	}
}

func TestBudgetExhaustionMarksUnproven(t *testing.T) {
	res, err := taskpkg.Reduce(hostTree(), hostValidate, hostExecute, taskpkg.Budget{MaxExecutions: 1})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if res.ProvenMinimal {
		t.Fatal("expected unproven result when the execution budget is exhausted")
	}
	if res.NodeCount != 5 {
		t.Fatalf("node count = %d, want the original 5", res.NodeCount)
	}

	res, err = taskpkg.Reduce(hostTree(), hostValidate, hostExecute, taskpkg.Budget{MaxCandidates: 4})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if res.ProvenMinimal {
		t.Fatal("expected unproven result when the candidate budget is exhausted")
	}
}

func TestCheckShapeLimits(t *testing.T) {
	wide := &taskpkg.Node{Name: "r"}
	for i := 0; i <= taskpkg.MaxOptionalNodes; i++ {
		wide.Children = append(wide.Children, &taskpkg.Node{Name: fmt.Sprintf("n%d", i), Optional: true})
	}
	if err := taskpkg.CheckShape(wide); err == nil {
		t.Fatal("expected error for more than 16 optional nodes")
	}

	many := &taskpkg.Node{
		Name: "r",
		Fields: []taskpkg.IntField{
			{Name: "x", Value: 0, Candidates: []uint64{0, 1, 2, 3, 4, 5, 6, 7, 8}},
		},
	}
	if err := taskpkg.CheckShape(many); err == nil {
		t.Fatal("expected error for more than 8 candidate values")
	}

	bad := &taskpkg.Node{
		Name: "r",
		Refs: []taskpkg.Ref{{Name: "p", Target: "nowhere"}},
	}
	if err := taskpkg.CheckShape(bad); err == nil {
		t.Fatal("expected error for unknown ref target")
	}
}

func TestOriginalMustReproduce(t *testing.T) {
	tree := hostTree()
	tree.Children[1].Fields[0].Value = 64 // length 64: payload read is in bounds
	tree.Children[1].Fields[0].Candidates = []uint64{64, 8}
	// Validation rejects length > 48, so use a permissive validator here.
	_, err := taskpkg.Reduce(tree, func([]byte) error { return nil }, hostExecute, taskpkg.Budget{})
	if err == nil {
		t.Fatal("expected error when the original input does not reproduce the OOB read")
	}
}
