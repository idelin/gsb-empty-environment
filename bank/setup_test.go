package bank_test

import (
	"fmt"
	"strings"
	"testing"

	"outboxsim/bank"
	"outboxsim/sim"
	"outboxsim/store"
)

func commands(n int) []bank.Command {
	cs := make([]bank.Command, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("c%d", i+1)
		cs[i] = bank.Command{CmdID: id, Acct: "A", Amount: int64(10 * (i + 1))}
	}
	return cs
}

// rig 封装一次完整模拟。
type rig struct {
	s      *sim.Sim
	svcDB  *store.DB
	dsDB   *store.DB
	client *bank.Client
}

func newRig(seed int64, cmds []bank.Command) *rig {
	s := sim.New(seed)
	svcDB := store.New()
	dsDB := store.New()
	svc := bank.NewService(svcDB, s.Bus, func(ids []string) {
		s.RecordTrace("commit", bank.NodeSvc, strings.Join(ids, " "))
	})
	ds := bank.NewDownstream(dsDB, s.Bus)
	client := bank.NewClient("client", s.Bus, cmds)

	s.AddNode(bank.NodeSvc, svc).Boot()
	s.AddNode(bank.NodeDS, ds).Boot()
	s.AddNode("client", client).Boot()

	s.SetInvariant(func(x *sim.Sim) { bank.AssertSafety(svcDB, dsDB, x) })
	return &rig{s: s, svcDB: svcDB, dsDB: dsDB, client: client}
}

func (r *rig) run(t *testing.T, cap int) int64 {
	t.Helper()
	ok := r.s.RunUntil(func(*sim.Sim) bool {
		return r.finished()
	}, cap)
	if !ok {
		t.Fatalf("liveness: did not finish within %d events; traces:\n%s",
			cap, dump(r.s.Traces()))
	}
	return r.s.Now
}

// finished 表示活性终点：所有命令被响应、所有 outbox sent、所有效果落地。
func (r *rig) finished() bool {
	if !r.client.Done() {
		return false
	}
	allSent := true
	for _, k := range r.svcDB.ViewKeys(keyOutboxP) {
		id := strings.TrimPrefix(k, keyOutboxP)
		if st, ok := bank.OutboxStatus(r.svcDB, id); !ok || st != "sent" {
			allSent = false
		}
	}
	return allSent &&
		bank.CountPrefix(r.svcDB, keyOutboxP) == bank.CountPrefix(r.dsDB, keyEffectP)
}

func (r *rig) assertFinal(t *testing.T, wantSum int64) {
	t.Helper()
	if got := bank.Balance(r.svcDB, "A"); got != wantSum {
		t.Fatalf("balance = %d, want %d", got, wantSum)
	}
	if bank.CountPrefix(r.svcDB, keyInboxP) != len(r.client.DoneReceipts()) {
		t.Fatalf("inbox count mismatch")
	}
	if bank.CountPrefix(r.dsDB, keyEffectP) != bank.CountPrefix(r.svcDB, keyOutboxP) {
		t.Fatalf("effects != outbox rows")
	}
}

const (
	keyInboxP  = "inbox:"
	keyOutboxP = "outbox:"
	keyEffectP = "effect:"
)

func dump(ts []sim.Trace) string {
	var b strings.Builder
	for _, x := range ts {
		switch {
		case x.Msg != nil:
			fmt.Fprintf(&b, "  t%-4d %-8s %s\n", x.Tick, x.Kind, x.Msg)
		case x.Label != "":
			fmt.Fprintf(&b, "  t%-4d %-8s %s %s\n", x.Tick, x.Kind, x.Node, x.Label)
		default:
			fmt.Fprintf(&b, "  t%-4d %-8s %s\n", x.Tick, x.Kind, x.Node)
		}
	}
	return b.String()
}
