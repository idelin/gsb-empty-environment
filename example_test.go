package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0142"
)

func ExampleMigrator() {
	src := netsec.NewSource("vm-1", "vm-1/src/1")
	dst := netsec.NewTarget("vm-1", "vm-1/dst/1")
	signer, _ := netsec.NewSigner(make([]byte, 32))
	host, _ := netsec.NewMigrator(src, dst, signer)

	_, _ = src.WritePage(0, []byte("kernel"))
	_, _ = host.RunPreCopy(4)
	handoff, _ := host.Cutover(netsec.DeviceState{PolicyVersion: 1, KeyEpoch: 2})
	confirmation, _ := host.StartTarget()
	_ = host.ConfirmStartup(confirmation)

	memory, _ := dst.ReadPage(0)
	fmt.Println(string(memory), handoff.Manifest.Epoch > 0, src.Terminated())
	// Output: kernel true true
}
