package bufauth_test

import (
	"errors"
	"fmt"
	"syscall"

	"example.org/netsec/q0028/bufauth"
)

func ExampleAuthority_Forward() {
	auth := bufauth.NewAuthority()

	// The owner grants full rights to the proxy for one buffer.
	root, err := auth.Issue("framebuffer-7", bufauth.PermRead|bufauth.PermWrite|bufauth.PermReexport|bufauth.PermMap)
	if err != nil {
		fmt.Println("issue:", err)
		return
	}

	// A less privileged peer may only read and reexport the buffer.
	peer, err := auth.Forward(root, bufauth.PermRead|bufauth.PermReexport)
	if err != nil {
		fmt.Println("forward:", err)
		return
	}

	// Escalation attempts are rejected with typed errors.
	if _, err := auth.Forward(peer, bufauth.PermRead|bufauth.PermWrite); errors.Is(err, bufauth.ErrPermEscalation) {
		fmt.Println("read-only cannot gain write")
	}
	leaf, _ := auth.Forward(peer, bufauth.PermRead)
	if _, err := auth.Forward(leaf, bufauth.PermRead); errors.Is(err, bufauth.ErrReexportDenied) {
		fmt.Println("chain stops without reexport")
	}

	fmt.Printf("root=%s peer=%s parent=%d\n", root.Perms(), peer.Perms(), peer.ParentID())
	// Output:
	// read-only cannot gain write
	// chain stops without reexport
	// root=rwxm peer=rx parent=1
}

func ExampleAuthority_Revoke() {
	auth := bufauth.NewAuthority()
	root, _ := auth.Issue("framebuffer-7", bufauth.PermRead|bufauth.PermWrite|bufauth.PermReexport)
	child, _ := auth.Forward(root, bufauth.PermRead|bufauth.PermReexport)

	var pipe [2]int
	if err := syscall.Pipe(pipe[:]); err != nil {
		fmt.Println("pipe:", err)
		return
	}
	defer syscall.Close(pipe[0])
	defer syscall.Close(pipe[1])

	transfer, err := auth.BeginTransfer(child, "framebuffer-7", pipe[1])
	if err != nil {
		fmt.Println("transfer:", err)
		return
	}

	report, err := auth.Revoke(root)
	if err != nil {
		fmt.Println("revoke:", err)
		return
	}
	fmt.Println("revoked grant ids:", report.RevokedIDs)
	fmt.Println("reclaimed tokens:", len(report.ReclaimedTokens))
	fmt.Println("child live:", child.Live())

	if _, err := auth.Claim(transfer.Token()); errors.Is(err, bufauth.ErrTransferGone) {
		fmt.Println("pending transfer reclaimed")
	}
	// Output:
	// revoked grant ids: [1 2]
	// reclaimed tokens: 1
	// child live: false
	// pending transfer reclaimed
}
