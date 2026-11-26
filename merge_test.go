package seccomp

import "testing"

func TestMergeRules(t *testing.T) {
	cases := []struct {
		name string
		got  uint32
		want uint32
	}{
		{"numeric errno beats allow", MergeNumeric(RetAllow, RetErrno|1), RetErrno | 1},
		{"numeric kill beats errno", MergeNumeric(RetErrno|1, RetKillThread), RetKillThread},
		{"numeric killproc quirk loses to allow", MergeNumeric(RetKillProcess, RetAllow), RetAllow},
		{"kernel killproc wins over allow", MergeKernel(RetKillProcess, RetAllow), RetKillProcess},
		{"kernel killproc wins over errno", MergeKernel(RetKillProcess, RetErrno|13), RetKillProcess},
		{"kernel first-seen errno data", MergeKernel(RetErrno|1, RetErrno|13), RetErrno | 1},
		{"kernel first-seen same class", MergeKernel(RetKillProcess, RetKillThread), RetKillProcess},
		{"kernel thread first keeps thread", MergeKernel(RetKillThread, RetKillProcess), RetKillThread},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %#x (%s), want %#x (%s)", tc.got, ActionName(tc.got), tc.want, ActionName(tc.want))
			}
		})
	}
}

func nrEqActionFilter(nr, action uint32) []Instruction {
	return []Instruction{
		Stmt(opLdNR, OffNR),
		Jump(JmpJEQ|SrcK|ClassJMP, nr, 0, 1),
		Stmt(opRetK, action),
		Stmt(opRetK, RetAllow),
	}
}

func TestStackNumericKillProcessQuirk(t *testing.T) {
	pol := &Policy{Rules: []Rule{Deny("execve", []Arch{ArchX8664}, []uint32{59})}}
	killProc := nrEqActionFilter(59, RetKillProcess)
	allowAll := []Instruction{Stmt(opRetK, RetAllow)}

	rep, err := VerifyStack([][]Instruction{killProc, allowAll}, pol)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Safe {
		t.Fatal("raw numeric min treats KILL_PROCESS(0x80000000) > ALLOW and must report the numeric-rule bypass")
	}
	c := rep.Minimal
	if c.MergedAction&RetActionFull != RetAllow {
		t.Fatalf("merged = %s, want numeric ALLOW", ActionName(c.MergedAction))
	}
	if len(c.LayerActions) != 2 ||
		c.LayerActions[0]&RetActionFull != RetKillProcess ||
		c.LayerActions[1]&RetActionFull != RetAllow {
		t.Fatalf("layer actions = %v", c.LayerActions)
	}

	krep, err := VerifyStack([][]Instruction{killProc, allowAll}, pol, WithKernelMerge())
	if err != nil {
		t.Fatal(err)
	}
	if !krep.Safe {
		t.Fatalf("kernel action-mask merge must keep KILL_PROCESS, got bypass %s merged=%s",
			krep.Minimal.Tuple, ActionName(krep.Minimal.MergedAction))
	}
}

func TestStackDenyLayerProtects(t *testing.T) {
	pol := execvePolicy()
	weak := nr59OnlyFilter()

	// KILL_THREAD(0) numerically dominates every other return, so the strict
	// layer protects the stack under the raw numeric-min rule.
	strictThread := strictExecveFilterThread(t)
	rep, err := VerifyStack([][]Instruction{strictThread, weak}, pol)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Safe {
		t.Fatalf("kill-thread strict layer protects all archs numerically, got %s", rep.Minimal.Tuple)
	}

	// KILL_PROCESS needs the kernel action-mask merge to dominate ALLOW.
	strict := strictExecveFilter(t)
	krep, err := VerifyStack([][]Instruction{strict, weak}, pol, WithKernelMerge())
	if err != nil {
		t.Fatal(err)
	}
	if !krep.Safe {
		t.Fatalf("kernel-merge strict layer protects all archs, got %s", krep.Minimal.Tuple)
	}

	// Under raw numeric min the KILL_PROCESS high-bit quirk is observable.
	rep, err = VerifyStack([][]Instruction{strict, weak}, pol)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Safe {
		t.Fatal("numeric min exposes KILL_PROCESS(0x80000000) vs ALLOW quirk")
	}
}

func TestStackBothVulnerableFindsBypass(t *testing.T) {
	pol := execvePolicy()
	f1 := nr59OnlyFilter()
	f2 := nrEqActionFilter(SysPtraceX8664, RetErrno|1) // unrelated rule, allows 11
	rep, err := VerifyStack([][]Instruction{f1, f2}, pol)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Safe {
		t.Fatal("i386/11 is allowed by both layers; stack should be bypassable")
	}
	if rep.Minimal.Tuple.Arch != ArchI386 {
		t.Fatalf("minimal stack witness = %s, want i386", rep.Minimal.Tuple)
	}
	for _, a := range rep.Minimal.LayerActions {
		if !IsLaxAction(a, false) {
			t.Fatalf("layer action %s not lax", ActionName(a))
		}
	}
}
