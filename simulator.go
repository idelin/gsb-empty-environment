package main

import (
	"fmt"
	"sort"
	"strings"
)

const (
	NodeA = "A"
	NodeB = "B"

	EventWrite   = "write"
	EventRead    = "read"
	EventSend    = "send"
	EventDeliver = "deliver"
	EventDrop    = "drop"
	EventClock   = "clock"
	EventCrash   = "crash"
	EventRecover = "recover"
)

type Event struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Node     string `json:"node,omitempty"`
	OpID     string `json:"op_id,omitempty"`
	Key      string `json:"key,omitempty"`
	Value    string `json:"value,omitempty"`
	Expected string `json:"expected,omitempty"`
	To       string `json:"to,omitempty"`
	MsgID    string `json:"msg_id,omitempty"`
	At       int64  `json:"at"`
	Deadline int64  `json:"deadline,omitempty"`
}

type Violation struct {
	Kind     string `json:"kind"`
	EventID  string `json:"event_id,omitempty"`
	OpID     string `json:"op_id,omitempty"`
	Node     string `json:"node,omitempty"`
	Key      string `json:"key,omitempty"`
	Expected string `json:"expected,omitempty"`
	Got      string `json:"got,omitempty"`
	At       int64  `json:"at"`
}

type MessageStatus struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	To       string `json:"to"`
	OpID     string `json:"op_id,omitempty"`
	Key      string `json:"key,omitempty"`
	Value    string `json:"value,omitempty"`
	Status   string `json:"status"`
	Dropped  bool   `json:"dropped"`
	Received bool   `json:"received"`
}

type OperationStatus struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Node        string `json:"node"`
	Key         string `json:"key"`
	Value       string `json:"value,omitempty"`
	Expected    string `json:"expected,omitempty"`
	StartedAt   int64  `json:"started_at"`
	Deadline    int64  `json:"deadline,omitempty"`
	Completed   bool   `json:"completed"`
	TimedOut    bool   `json:"timed_out"`
	CompletedAt int64  `json:"completed_at,omitempty"`
}

type Replica struct {
	Node  string            `json:"node"`
	Up    bool              `json:"up"`
	Store map[string]string `json:"store"`
}

type Report struct {
	Now          int64                      `json:"now"`
	Replicas     map[string]Replica         `json:"replicas"`
	Messages     map[string]MessageStatus   `json:"messages"`
	Operations   map[string]OperationStatus `json:"operations"`
	Acknowledged []AcknowledgedWrite        `json:"acknowledged"`
	Safety       []Violation                `json:"safety_violations"`
	Liveness     []Violation                `json:"liveness_violations"`
}

type AcknowledgedWrite struct {
	OpID  string `json:"op_id"`
	Key   string `json:"key"`
	Value string `json:"value"`
	At    int64  `json:"at"`
	Node  string `json:"node"`
}

type InvalidEventError struct {
	EventID string
	Reason  string
}

func (e InvalidEventError) Error() string {
	if e.EventID == "" {
		return "invalid trace: " + e.Reason
	}
	return fmt.Sprintf("invalid event %s: %s", e.EventID, e.Reason)
}

type runtime struct {
	now      int64
	up       map[string]bool
	store    map[string]map[string]string
	queues   map[string][]MessageStatus
	messages map[string]MessageStatus
	ops      map[string]*OperationStatus
	acks     []AcknowledgedWrite
	safety   []Violation
	liveness []Violation
	seen     map[string]bool
}

func Replay(events []Event) (*Report, error) {
	rt := &runtime{
		now: 0,
		up:  map[string]bool{NodeA: true, NodeB: true},
		store: map[string]map[string]string{
			NodeA: {},
			NodeB: {},
		},
		queues:   map[string][]MessageStatus{NodeA: nil, NodeB: nil},
		messages: map[string]MessageStatus{},
		ops:      map[string]*OperationStatus{},
		seen:     map[string]bool{},
	}

	for i := range events {
		ev := events[i]
		if err := rt.apply(ev); err != nil {
			return nil, err
		}
	}
	if err := rt.checkCausalDelivery(events); err != nil {
		return nil, err
	}
	rt.timeOutPending()
	return rt.report(), nil
}

func (rt *runtime) apply(ev Event) error {
	if ev.ID == "" {
		return InvalidEventError{Reason: "event id is required"}
	}
	if rt.seen[ev.ID] {
		return InvalidEventError{EventID: ev.ID, Reason: "duplicate event id"}
	}
	rt.seen[ev.ID] = true
	if ev.At < rt.now {
		return InvalidEventError{EventID: ev.ID, Reason: "event time moves backwards"}
	}
	if ev.Type != EventClock {
		if ev.At != rt.now {
			return InvalidEventError{EventID: ev.ID, Reason: "only clock events may advance time"}
		}
	}
	switch ev.Type {
	case EventWrite:
		return rt.write(ev)
	case EventRead:
		return rt.read(ev)
	case EventSend:
		return rt.send(ev)
	case EventDeliver:
		return rt.deliver(ev)
	case EventDrop:
		return rt.drop(ev)
	case EventClock:
		return rt.clock(ev)
	case EventCrash:
		return rt.crash(ev)
	case EventRecover:
		return rt.recover(ev)
	default:
		return InvalidEventError{EventID: ev.ID, Reason: "unknown event type"}
	}
}

func validNode(node string) bool {
	return node == NodeA || node == NodeB
}

func nonempty(ev Event, field, value string) error {
	if value == "" {
		return InvalidEventError{EventID: ev.ID, Reason: field + " is required"}
	}
	return nil
}

func (rt *runtime) write(ev Event) error {
	if err := nonempty(ev, "node", ev.Node); err != nil {
		return err
	}
	if err := nonempty(ev, "op_id", ev.OpID); err != nil {
		return err
	}
	if err := nonempty(ev, "key", ev.Key); err != nil {
		return err
	}
	if err := nonempty(ev, "value", ev.Value); err != nil {
		return err
	}
	if !validNode(ev.Node) {
		return InvalidEventError{EventID: ev.ID, Reason: "unknown node"}
	}
	if _, exists := rt.ops[ev.OpID]; exists {
		return InvalidEventError{EventID: ev.ID, Reason: "duplicate operation id"}
	}
	if ev.Deadline != 0 && ev.Deadline <= ev.At {
		return InvalidEventError{EventID: ev.ID, Reason: "deadline must be after start"}
	}
	op := &OperationStatus{ID: ev.OpID, Kind: EventWrite, Node: ev.Node, Key: ev.Key, Value: ev.Value, StartedAt: ev.At, Deadline: ev.Deadline}
	rt.ops[ev.OpID] = op
	if !rt.up[ev.Node] {
		return nil
	}
	rt.store[ev.Node][ev.Key] = ev.Value
	op.Completed = true
	op.CompletedAt = ev.At
	rt.acks = append(rt.acks, AcknowledgedWrite{OpID: ev.OpID, Key: ev.Key, Value: ev.Value, At: ev.At, Node: ev.Node})
	return nil
}

func (rt *runtime) read(ev Event) error {
	if err := nonempty(ev, "node", ev.Node); err != nil {
		return err
	}
	if err := nonempty(ev, "op_id", ev.OpID); err != nil {
		return err
	}
	if err := nonempty(ev, "key", ev.Key); err != nil {
		return err
	}
	if !validNode(ev.Node) {
		return InvalidEventError{EventID: ev.ID, Reason: "unknown node"}
	}
	if _, exists := rt.ops[ev.OpID]; exists {
		return InvalidEventError{EventID: ev.ID, Reason: "duplicate operation id"}
	}
	if ev.Deadline != 0 && ev.Deadline <= ev.At {
		return InvalidEventError{EventID: ev.ID, Reason: "deadline must be after start"}
	}
	if err := rt.validateCrossNodeRead(ev); err != nil {
		return err
	}
	op := &OperationStatus{ID: ev.OpID, Kind: EventRead, Node: ev.Node, Key: ev.Key, Expected: ev.Expected, StartedAt: ev.At, Deadline: ev.Deadline}
	rt.ops[ev.OpID] = op
	if rt.up[ev.Node] {
		rt.completeRead(ev, op)
	}
	return nil
}

func (rt *runtime) completeRead(ev Event, op *OperationStatus) {
	got := rt.store[ev.Node][ev.Key]
	op.Completed = true
	op.CompletedAt = ev.At
	op.Value = got
	if ev.Expected != got {
		rt.safety = append(rt.safety, Violation{
			Kind: "stale_read", EventID: ev.ID, OpID: ev.OpID, Node: ev.Node,
			Key: ev.Key, Expected: ev.Expected, Got: got, At: ev.At,
		})
	}
}

func (rt *runtime) send(ev Event) error {
	if err := nonempty(ev, "node", ev.Node); err != nil {
		return err
	}
	if err := nonempty(ev, "to", ev.To); err != nil {
		return err
	}
	if err := nonempty(ev, "msg_id", ev.MsgID); err != nil {
		return err
	}
	if err := nonempty(ev, "op_id", ev.OpID); err != nil {
		return err
	}
	if err := nonempty(ev, "key", ev.Key); err != nil {
		return err
	}
	if err := nonempty(ev, "value", ev.Value); err != nil {
		return err
	}
	if !validNode(ev.Node) || !validNode(ev.To) || ev.Node == ev.To {
		return InvalidEventError{EventID: ev.ID, Reason: "send must name two different replicas"}
	}
	if _, exists := rt.messages[ev.MsgID]; exists {
		return InvalidEventError{EventID: ev.ID, Reason: "duplicate message id"}
	}
	op, exists := rt.ops[ev.OpID]
	if !exists || op.Kind != EventWrite {
		return InvalidEventError{EventID: ev.ID, Reason: "send requires a prior write operation"}
	}
	if op.Node != ev.Node || op.Key != ev.Key || op.Value != ev.Value {
		return InvalidEventError{EventID: ev.ID, Reason: "send payload does not match its write"}
	}
	if !op.Completed {
		return InvalidEventError{EventID: ev.ID, Reason: "cannot send for an unacknowledged write"}
	}
	rt.messages[ev.MsgID] = MessageStatus{
		ID: ev.MsgID, From: ev.Node, To: ev.To, OpID: ev.OpID,
		Key: ev.Key, Value: ev.Value, Status: "scheduled",
	}
	return nil
}

func (rt *runtime) deliver(ev Event) error {
	if err := nonempty(ev, "msg_id", ev.MsgID); err != nil {
		return err
	}
	msg, exists := rt.messages[ev.MsgID]
	if !exists {
		return InvalidEventError{EventID: ev.ID, Reason: "deliver requires a prior send"}
	}
	if msg.Status == "delivered" || msg.Received {
		return InvalidEventError{EventID: ev.ID, Reason: "message already delivered"}
	}
	if msg.Dropped {
		msg.Status = "suppressed"
		rt.messages[ev.MsgID] = msg
		return nil
	}
	msg.Received = true
	msg.Status = "delivered"
	rt.messages[ev.MsgID] = msg
	if rt.up[msg.To] {
		rt.store[msg.To][msg.Key] = msg.Value
		return nil
	}
	rt.queues[msg.To] = append(rt.queues[msg.To], msg)
	return nil
}

func (rt *runtime) drop(ev Event) error {
	if err := nonempty(ev, "msg_id", ev.MsgID); err != nil {
		return err
	}
	msg, exists := rt.messages[ev.MsgID]
	if !exists {
		return InvalidEventError{EventID: ev.ID, Reason: "drop requires a prior send"}
	}
	if msg.Received || msg.Status == "delivered" {
		return InvalidEventError{EventID: ev.ID, Reason: "cannot drop an already delivered message"}
	}
	if msg.Dropped {
		return InvalidEventError{EventID: ev.ID, Reason: "message already dropped"}
	}
	msg.Dropped = true
	msg.Status = "dropped"
	rt.messages[ev.MsgID] = msg
	return nil
}

func (rt *runtime) clock(ev Event) error {
	if ev.At <= rt.now {
		return InvalidEventError{EventID: ev.ID, Reason: "clock must strictly advance"}
	}
	rt.now = ev.At
	rt.timeOutPending()
	return nil
}

func (rt *runtime) crash(ev Event) error {
	if err := nonempty(ev, "node", ev.Node); err != nil {
		return err
	}
	if !validNode(ev.Node) {
		return InvalidEventError{EventID: ev.ID, Reason: "unknown node"}
	}
	if !rt.up[ev.Node] {
		return InvalidEventError{EventID: ev.ID, Reason: "replica is already crashed"}
	}
	rt.up[ev.Node] = false
	return nil
}

func (rt *runtime) recover(ev Event) error {
	if err := nonempty(ev, "node", ev.Node); err != nil {
		return err
	}
	if !validNode(ev.Node) {
		return InvalidEventError{EventID: ev.ID, Reason: "unknown node"}
	}
	if rt.up[ev.Node] {
		return InvalidEventError{EventID: ev.ID, Reason: "replica is already up"}
	}
	rt.up[ev.Node] = true
	queued := rt.queues[ev.Node]
	rt.queues[ev.Node] = nil
	for _, queuedMessage := range queued {
		if current := rt.messages[queuedMessage.ID]; current.Dropped {
			continue
		}
		rt.store[ev.Node][queuedMessage.Key] = queuedMessage.Value
	}
	opIDs := make([]string, 0, len(rt.ops))
	for id := range rt.ops {
		opIDs = append(opIDs, id)
	}
	sort.Strings(opIDs)
	for _, id := range opIDs {
		op := rt.ops[id]
		if op.Node == ev.Node && !op.Completed && !op.TimedOut {
			op.Completed = true
			op.CompletedAt = ev.At
			if op.Kind == EventRead {
				op.Value = rt.store[ev.Node][op.Key]
				if op.Expected != op.Value {
					rt.safety = append(rt.safety, Violation{
						Kind: "stale_read", EventID: "recover:" + op.ID, OpID: op.ID,
						Node: ev.Node, Key: op.Key, Expected: op.Expected,
						Got: op.Value, At: ev.At,
					})
				}
			}
		}
	}
	return nil
}

func (rt *runtime) timeOutPending() {
	ids := make([]string, 0, len(rt.ops))
	for id := range rt.ops {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		op := rt.ops[id]
		if !op.Completed && !op.TimedOut && op.Deadline != 0 && rt.now >= op.Deadline {
			op.TimedOut = true
			rt.liveness = append(rt.liveness, Violation{
				Kind: "deadline_exceeded", OpID: op.ID, Node: op.Node,
				Key: op.Key, At: op.Deadline,
			})
		}
	}
}

func (rt *runtime) validateCrossNodeRead(ev Event) error {
	if ev.Expected == "" {
		return nil
	}
	var source *AcknowledgedWrite
	for i := range rt.acks {
		ack := rt.acks[i]
		if ack.Key == ev.Key && ack.Value == ev.Expected {
			source = &ack
		}
	}
	if source == nil {
		return InvalidEventError{EventID: ev.ID, Reason: "read expects a value with no prior acknowledged write"}
	}
	if source.Node == ev.Node {
		return nil
	}
	ids := make([]string, 0)
	for id, msg := range rt.messages {
		if msg.OpID == source.OpID && msg.Key == source.Key && msg.Value == source.Value && msg.To == ev.Node {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return InvalidEventError{EventID: ev.ID, Reason: "cross-node read removes required replication/drop causal path"}
	}
	msg := rt.messages[ids[0]]
	if !msg.Dropped && !msg.Received {
		return InvalidEventError{EventID: ev.ID, Reason: "replication message removes its delivery or fault step"}
	}
	return nil
}

func (rt *runtime) checkCausalDelivery(events []Event) error {
	for _, drop := range events {
		if drop.Type != EventDrop {
			continue
		}
		hasDelivery := false
		for _, ev := range events {
			if ev.Type == EventDeliver && ev.MsgID == drop.MsgID {
				hasDelivery = true
			}
		}
		if !hasDelivery {
			return InvalidEventError{EventID: drop.ID, Reason: "drop fault requires its scheduled delivery event"}
		}
	}
	return nil
}

func (rt *runtime) report() *Report {
	replicas := make(map[string]Replica, len(rt.up))
	for node, up := range rt.up {
		storeCopy := make(map[string]string, len(rt.store[node]))
		for key, value := range rt.store[node] {
			storeCopy[key] = value
		}
		replicas[node] = Replica{Node: node, Up: up, Store: storeCopy}
	}
	messages := make(map[string]MessageStatus, len(rt.messages))
	for id, msg := range rt.messages {
		messages[id] = msg
	}
	operations := make(map[string]OperationStatus, len(rt.ops))
	for id, op := range rt.ops {
		operations[id] = *op
	}
	acks := append([]AcknowledgedWrite(nil), rt.acks...)
	safety := append([]Violation(nil), rt.safety...)
	liveness := append([]Violation(nil), rt.liveness...)
	return &Report{
		Now: rt.now, Replicas: replicas, Messages: messages, Operations: operations,
		Acknowledged: acks, Safety: safety, Liveness: liveness,
	}
}

func (r *Report) FailureClass() string {
	switch {
	case len(r.Safety) > 0:
		return "safety"
	case len(r.Liveness) > 0:
		return "liveness"
	default:
		return "no_failure"
	}
}

func safetyFingerprint(violations []Violation) string {
	parts := make([]string, 0, len(violations))
	for _, violation := range violations {
		parts = append(parts, strings.Join([]string{
			violation.Kind, violation.Key, violation.Node, violation.Expected, violation.Got,
		}, "|"))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}
