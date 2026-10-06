// Command sim runs a scripted single-node replication scenario against a
// durable store, demonstrating term changes, suffix truncation, duplicate
// delivery, crash cut points and commit advancement.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"logsim/replica"
)

type crash struct{ point string }

type crashPanic struct{}

func (c *crash) hook(point string) error {
	if point == c.point {
		c.point = ""
		panic(crashPanic{})
	}
	return nil
}

func report(tag string, rep replica.AppendReply, err error) {
	if err != nil {
		fmt.Printf("%-28s -> ERROR: %v\n", tag, err)
		return
	}
	status := "ACK"
	if !rep.Success {
		status = "REJECT(" + rep.Reason + ")"
	}
	fmt.Printf("%-28s -> %-22s term=%d last=%d commit=%d\n",
		tag, status, rep.Term, rep.LastIndex, rep.CommitIndex)
}

func state(r *replica.Replica) {
	term, commit, last, entries := r.State()
	fmt.Printf("    state: term=%d commit=%d last=%d log=%v\n", term, commit, last, entries)
}

func do(r *replica.Replica, tag string, req replica.AppendRequest) {
	rep, err := r.HandleAppend(req)
	report(tag, rep, err)
}

func main() {
	dir := filepath.Join(os.TempDir(), "logsim-demo")
	os.RemoveAll(dir)
	open := func(hook replica.Hook) *replica.Replica {
		st, err := replica.OpenStore(dir, hook)
		if err != nil {
			log.Fatal(err)
		}
		r, err := replica.OpenReplica(st)
		if err != nil {
			log.Fatal(err)
		}
		return r
	}

	fmt.Println("== term 1: initial append, commit 1 ==")
	r := open(nil)
	do(r, "append a,b,c commit=1", replica.AppendRequest{
		Term: 1, Entries: []replica.Entry{{Term: 1, Command: "a"}, {Term: 1, Command: "b"}, {Term: 1, Command: "c"}},
		LeaderCommit: 1})
	state(r)

	fmt.Println("== duplicate redelivery (idempotent) ==")
	do(r, "append a,b,c again", replica.AppendRequest{
		Term: 1, Entries: []replica.Entry{{Term: 1, Command: "a"}, {Term: 1, Command: "b"}, {Term: 1, Command: "c"}},
		LeaderCommit: 1})

	fmt.Println("== missing predecessor rejected ==")
	do(r, "append prev=6 (gap)", replica.AppendRequest{
		Term: 1, PrevIndex: 6, PrevTerm: 1, Entries: []replica.Entry{{Term: 1, Command: "g"}}})

	fmt.Println("== crash before ack on entry d ==")
	cj := &crash{point: replica.PointSaveBegin}
	r = open(cj.hook)
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				fmt.Println("    *** power loss before persist: no ack sent ***")
			}
		}()
		r.HandleAppend(replica.AppendRequest{Term: 1, PrevIndex: 3, PrevTerm: 1,
			Entries: []replica.Entry{{Term: 1, Command: "d"}}, LeaderCommit: 3})
	}()
	r = open(nil)
	state(r)

	fmt.Println("== term 2: truncate uncommitted suffix, commit 3 ==")
	do(r, "append B,C @idx2 term2", replica.AppendRequest{
		Term: 2, PrevIndex: 1, PrevTerm: 1,
		Entries:      []replica.Entry{{Term: 2, Command: "B"}, {Term: 2, Command: "C"}},
		LeaderCommit: 3})
	state(r)

	fmt.Println("== stale term 1 message rejected ==")
	do(r, "append term=1", replica.AppendRequest{
		Term: 1, PrevIndex: 3, PrevTerm: 2, Entries: []replica.Entry{{Term: 1, Command: "z"}}})

	fmt.Println("== committed prefix protected ==")
	_, err := r.HandleAppend(replica.AppendRequest{Term: 3, PrevIndex: 0, PrevTerm: 0,
		Entries: []replica.Entry{{Term: 3, Command: "X"}}})
	report("overwrite committed idx1", replica.AppendReply{}, err)

	fmt.Println("== same index+term, different content ==")
	_, err = r.HandleAppend(replica.AppendRequest{Term: 2, PrevIndex: 1, PrevTerm: 1,
		Entries: []replica.Entry{{Term: 2, Command: "BOGUS"}}})
	report("rewrite idx2 term2", replica.AppendReply{}, err)
	state(r)
}
