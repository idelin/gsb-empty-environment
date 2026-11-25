package permcompile

import "testing"

func TestTraceFullReplay(t *testing.T) {
	plan := &Plan{Root: &Node{
		Kind: KindChoice, On: "pick",
		Kids: []*Node{
			{Kind: KindLeaf, Opt: "a", Grant: &Grant{Object: "o", Gen: 1, Scope: ByteRange{0, 10}, MaxReads: 1}},
			{Kind: KindLeaf, Opt: "b", Grant: &Grant{Object: "o", Gen: 1, Scope: ByteRange{0, 10}, MaxReads: 1}},
		},
	}}
	it := newInterp(t, plan)

	it.rt.RecordAuth(req("o", 1, 0, 10), it.rt.Authorize(req("o", 1, 0, 10)))
	_ = it.rt.Commit(req("o", 1, 0, 10))
	if err := it.rt.Ingest(it.decision("pick", "a")); err != nil {
		t.Fatal(err)
	}

	entries := it.rt.Trace()
	if len(entries) != 3 {
		t.Fatalf("trace length = %d, want 3", len(entries))
	}
	wantKinds := []TraceKind{TraceAuth, TraceCommit, TraceEvent}
	for i, want := range wantKinds {
		if entries[i].Kind != want {
			t.Fatalf("trace[%d] = %s, want %s", i, entries[i].Kind, want)
		}
		if len(entries[i].Worlds) == 0 {
			t.Fatalf("trace[%d] has no world snapshots", i)
		}
	}
	if entries[0].Verdict != VerdictAllow {
		t.Fatalf("first trace verdict = %v", entries[0].Verdict)
	}
	if entries[2].WorldCount != 1 {
		t.Fatalf("post-decision world count = %d", entries[2].WorldCount)
	}
}
