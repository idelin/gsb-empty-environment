package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
)

// SynthEvents deterministically generates n events across the given number
// of tenants, with highRatio fraction marked high priority.
func SynthEvents(seed int64, n, tenants int, highRatio float64) []Event {
	r := rand.New(rand.NewSource(seed))
	events := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		priority := PriorityNormal
		if r.Float64() < highRatio {
			priority = PriorityHigh
		}
		events = append(events, Event{
			ID:       uint64(i + 1),
			Tenant:   fmt.Sprintf("tenant-%d", r.Intn(tenants)),
			Priority: priority,
			Payload:  fmt.Sprintf("m%d-%d", i, r.Intn(1<<20)),
		})
	}
	return events
}

// WriteFixture stores events as JSON lines so a run can be replayed exactly.
func WriteFixture(path string, events []Event) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return w.Flush()
}

// ReadFixture loads events from a JSON-lines fixture file.
func ReadFixture(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var events []Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("fixture: %w", err)
		}
		events = append(events, ev)
	}
	return events, sc.Err()
}
