package main

// Priority marks how urgently an event should be processed.
type Priority string

const (
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
)

// Event is a single telemetry record moving through the queue.
type Event struct {
	ID       uint64   `json:"id"`
	Tenant   string   `json:"tenant"`
	Priority Priority `json:"priority"`
	Payload  string   `json:"payload"`
}
