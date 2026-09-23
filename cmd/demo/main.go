// Demo: serialize an error graph with a shared cause, a cycle and
// sensitive context; decode it back; show the non-leakage rule.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"

	"errser"
)

func indent(t *json.RawMessage, data []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return string(data)
	}
	return buf.String()
}

func main() {
	allow := map[string]bool{"host": true, "field": true, "user": true}

	// A graph with a shared cause, an aggregate, sensitive context...
	shared := errser.New(errser.KindTimeout, "db dial timeout").
		With("host", "db.internal").
		With("password", "s3cret!") // sensitive: never emitted
	validation := errser.New(errser.KindValidation, "create user failed").
		With("field", "email").
		Wrap(shared)
	auth := errser.New(errser.KindAuth, "token expired").
		With("user", "alice").
		With("token", "tok-live-9f2"). // sensitive: never emitted
		Wrap(shared)
	root := errser.NewAggregate("batch import failed", validation, auth)

	// ...and a cycle: worker <-> supervisor.
	worker := errser.New(errser.KindInternal, "worker crashed")
	supervisor := errser.New(errser.KindInternal, "supervisor restart").Wrap(worker)
	worker.Wrap(supervisor)
	graph := errser.NewAggregate("system report", root, worker)

	enc := errser.Encoder{Allow: allow}
	data, err := enc.Encode(graph)
	if err != nil {
		panic(err)
	}
	fmt.Println("=== serialized envelope ===")
	var raw json.RawMessage = data
	fmt.Println(indent(&raw, data))

	decoded, err := errser.Decode(data, errser.Limits{})
	if err != nil {
		panic(err)
	}
	fmt.Println("=== decoded Error() (cycle-safe) ===")
	fmt.Println(decoded)

	// Identity: the shared cause is one object after decoding.
	agg := decoded.(*errser.Aggregate)
	batch := agg.Errors()[0].(*errser.Aggregate)
	viaValidation := batch.Errors()[0].(*errser.Error).Unwrap()
	viaAuth := batch.Errors()[1].(*errser.Error).Unwrap()
	fmt.Printf("=== shared cause identity preserved: %v ===\n", viaValidation == viaAuth)

	// Non-leakage: two inputs differing only in sensitive values.
	build := func(password string) []byte {
		e := errser.New(errser.KindAuth, "login failed").
			With("user", "alice").
			With("password", password)
		d, err := enc.Encode(e)
		if err != nil {
			panic(err)
		}
		return d
	}
	d1, d2 := build("hunter2"), build("correct-horse-battery-staple")
	fmt.Printf("=== non-leakage (sensitive-only diff => identical bytes): %v ===\n", string(d1) == string(d2))
	fmt.Println(string(d1))

	// Unknown kind on the wire: generic code, no host code executed.
	foreign := []byte(`{"v":1,"root":0,"nodes":[{"id":0,"kind":"os/exec.Command","msg":"rm -rf /"}]}`)
	safe, err := errser.Decode(foreign, errser.Limits{})
	if err != nil {
		panic(err)
	}
	fmt.Printf("=== unknown kind decodes to generic code: %q ===\n", safe)
}
