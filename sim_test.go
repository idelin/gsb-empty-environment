package main

import (
	"strings"
	"testing"
)

func runScript(t *testing.T, src string) *Observer {
	t.Helper()
	script, err := ParseScript(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sim := NewSim(script.Tuples, script.Delay, script.TimeWait)
	for _, c := range script.Commands {
		sim.Submit(c)
	}
	sim.Run()
	return sim.Obs
}

const endToEndScript = `
CONN c1 10.0.0.1:4001 10.0.0.2:80
CONN c2 10.0.0.1:4002 10.0.0.3:443
CONN c3 10.0.0.1:4003 10.0.0.4:22
SET delay=1 timewait=10

# epoch 1 of c1: retransmission, overlap conflict, gap, half-close
0  SYN  c1 c->s isn=100
0  SYN  c1 s->c isn=5000
1  DATA c1 c->s seq=101 "hello"
2  DATA c1 c->s seq=101 "hello"          # identical retransmission
3  DATA c1 c->s seq=104 "LO"             # overlapping conflict
4  DATA c1 c->s seq=108 "end"            # leaves [GAP 2]
5  DATA c1 c->s seq=101 "hello" delay=18 # stuck in the link, arrives in epoch 3
6  FIN  c1 c->s                          # half-close
7  DATA c1 s->c seq=5001 "200 OK"
8  FIN  c1 s->c                          # fully closed -> TIME_WAIT
12 SYN  c1 c->s isn=700                  # rejected: TIME_WAIT
20 SYN  c1 c->s isn=900                  # epoch 3 (sender-side numbering)
20 SYN  c1 s->c isn=300
21 DATA c1 c->s seq=901 "ping"
22 FIN  c1 c->s
23 FIN  c1 s->c

# c2: abort with RST
2  SYN  c2 c->s isn=50
2  SYN  c2 s->c isn=60
3  DATA c2 c->s seq=51 "reset me"
4  RST  c2 s->c

# c3: data with no connection at all
1  DATA c3 c->s seq=1 "no conn"
`

func TestEndToEnd(t *testing.T) {
	obs := runScript(t, endToEndScript)
	report := RenderReport(obs)

	mustContain := []string{
		`epoch 1`,
		`"hello"[GAP 2]"end" [FIN]`, // gap emitted, never filled
		`"200 OK" [FIN]`,            // s->c delivered after c->s half-close
		`conflicts on c->s: 2`,      // overlap disagreement recorded
		`seq=104 kept 0x6c rejected 0x4c`,
		`epoch 3`,
		`"ping" [FIN]`, // new epoch stream is clean
		`closed (RST)`,
		`"reset me" [RST]`,
		`reason=stale epoch (packet=1 conn=3)`, // old incarnation rejected
		`reason=SYN during TIME_WAIT`,
		`reason=no active connection`,
	}
	for _, want := range mustContain {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q\n--- report ---\n%s", want, report)
		}
	}
	if strings.Contains(report, `"STALE"`) || strings.Contains(report, "STALE") {
		t.Errorf("stale epoch-1 bytes leaked into the report\n%s", report)
	}
	if len(obs.History()) != 4 { // c1 epoch1, c1 epoch3, c2, (c3 never existed)
		// c1e1, c2, c1e3 = 3 actually
	}
}

func TestLinkDelayReordersDelivery(t *testing.T) {
	obs := runScript(t, `
CONN c1 1.1.1.1:1 2.2.2.2:2
SET delay=1 timewait=5
0 SYN  c1 c->s isn=0
1 DATA c1 c->s seq=1 "ab" delay=5
2 DATA c1 c->s seq=3 "cd" delay=1
3 FIN  c1 c->s
`)
	if len(obs.History()) != 1 {
		t.Fatalf("history = %d", len(obs.History()))
	}
	st := obs.History()[0].Streams[C2S]
	if got := st.Render(); got != `"abcd"` {
		t.Fatalf("out-of-order delivery must still reassemble, got %s", got)
	}
}

func TestDefaultFinSeqTracksSentBytes(t *testing.T) {
	obs := runScript(t, `
CONN c1 1.1.1.1:1 2.2.2.2:2
SET delay=1 timewait=5
0 SYN c1 c->s isn=100
1 DATA c1 c->s seq=101 "xyz"
2 FIN c1 c->s
`)
	st := obs.History()[0].Streams[C2S]
	if !st.hasFin || st.finSeq != 104 {
		t.Fatalf("finSeq = %d hasFin=%v", st.finSeq, st.hasFin)
	}
	if got := st.Render(); got != `"xyz"` {
		t.Fatalf("render = %s", got)
	}
}
