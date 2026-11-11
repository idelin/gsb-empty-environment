package shadow_test

import (
	"fmt"

	"example.org/netsec/uid1235/shadow"
)

func ExampleMachine_indirectJump() {
	machine := shadow.NewMachine("r0", "r1")
	target, err := machine.InputValue(0, shadow.Width16, 0x1004)
	if err != nil {
		panic(err)
	}
	if err := machine.WriteRegister("r0", target); err != nil {
		panic(err)
	}

	cleared, err := machine.Xor(
		machine.Const(shadow.Width16, 0x1004),
		machine.Const(shadow.Width16, 0x1004),
	)
	if err != nil {
		panic(err)
	}
	if err := machine.WriteRegister("r1", cleared); err != nil {
		panic(err)
	}

	jumpValue, err := machine.ReadRegister("r0", shadow.Width16)
	if err != nil {
		panic(err)
	}
	jump := machine.IndirectJump(jumpValue)
	sources, err := jump.Sources(2)
	if err != nil {
		panic(err)
	}

	fmt.Printf("address=%#x bit2=%v source=%v\n", jump.Address(), jump.BitSources()[2].Bit, sources.Slice())
	// Output: address=0x1004 bit2=1 source=[{0 2}]
}
