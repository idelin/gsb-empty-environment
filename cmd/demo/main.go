// Command demo shows serialization of an error graph containing a
// shared cause, a cycle, whitelisted context, and sensitive fields.
package main

import (
	"fmt"
	"log"

	"errser/errser"
)

func main() {
	errser.RegisterCode("db.timeout")
	errser.RegisterCode("api.fetch")

	opts := errser.DefaultOptions().AllowKeys("op", "retryable")

	shared := errser.New("db.timeout", "connection timed out").
		WithContext("op", "dial").
		WithContext("retryable", true).
		WithContext("password", "hunter2"). // sensitive: redacted
		WithContext("dsn", "postgres://u:p@db/app")

	a := errser.New("api.fetch", "fetch profile failed").SetCause(shared)
	b := errser.New("api.fetch", "fetch settings failed").SetCause(shared)
	root := errser.NewAggregate("", "request failed", a, b)

	// Introduce a cycle: the shared cause's own cause points back at a.
	shared.SetCause(a)

	data, err := errser.Marshal(root, opts)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("=== serialized document ===")
	fmt.Println(string(data))

	back, err := errser.Unmarshal(data, opts)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\n=== decoded graph (cycle-safe render) ===")
	fmt.Println(back.Error())

	kids := back.Children()
	fmt.Println("\n=== identity checks ===")
	fmt.Println("shared cause preserved:", kids[0].Cause() == kids[1].Cause())
	fmt.Println("cycle restored:", kids[0].Cause().Cause() == kids[0])

	// Non-leakage: same structure, different secret -> identical bytes.
	other := errser.New("db.timeout", "connection timed out").
		WithContext("op", "dial").
		WithContext("retryable", true).
		WithContext("password", "totally-different-secret").
		WithContext("dsn", "postgres://x:y@other/db")
	oa := errser.New("api.fetch", "fetch profile failed").SetCause(other)
	ob := errser.New("api.fetch", "fetch settings failed").SetCause(other)
	oroot := errser.NewAggregate("", "request failed", oa, ob)
	other.SetCause(oa)
	data2, err := errser.Marshal(oroot, opts)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\n=== non-leakage ===")
	fmt.Println("public output identical for different secrets:", string(data) == string(data2))
}
