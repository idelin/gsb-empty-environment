package seccomp_test

import (
	"fmt"

	seccomp "formal.local/w27-a4d6d2d9d3"
)

func ExampleVerify() {
	// A filter that blocks only the x86_64 execve number 59 without an
	// architecture check or the x32 bit mask.
	insns := []seccomp.Instruction{
		seccomp.Stmt(seccomp.ClassLD|seccomp.SizeW|seccomp.ModeABS, seccomp.OffNR),
		seccomp.Jump(seccomp.JmpJEQ|seccomp.SrcK|seccomp.ClassJMP, 59, 0, 1),
		seccomp.Stmt(seccomp.ClassRET|seccomp.RetK, seccomp.RetKillThread),
		seccomp.Stmt(seccomp.ClassRET|seccomp.RetK, seccomp.RetAllow),
	}
	policy := seccomp.NewPolicy(
		seccomp.Deny("execve-x86_64", []seccomp.Arch{seccomp.ArchX8664, seccomp.ArchX32}, []uint32{59}),
		seccomp.Deny("execve-i386", []seccomp.Arch{seccomp.ArchI386}, []uint32{11}),
	)
	rep, err := seccomp.Verify(insns, policy)
	if err != nil {
		panic(err)
	}
	fmt.Println("safe:", rep.Safe)
	fmt.Println("minimal arch:", rep.Minimal.Tuple.Arch)
	fmt.Println("minimal nr:", rep.Minimal.Tuple.NR)
	// Output:
	// safe: false
	// minimal arch: i386
	// minimal nr: 11
}

func ExampleDecode() {
	raw := []byte{
		0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x06, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0x7f,
	}
	insns, _ := seccomp.Decode(raw)
	fmt.Println(insns[0].String())
	fmt.Println(seccomp.ActionName(insns[1].K))
	// Output:
	// ldw A = seccomp[0]
	// ALLOW
}
