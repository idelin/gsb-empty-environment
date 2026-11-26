package devpolicy_test

import (
	"fmt"

	"formal.local/w3-063c1c73c6/devpolicy"
)

// Example shows the full workflow: compile a two-layer cgroup policy, audit
// it against a target specification and inspect the smallest escalation,
// then compare an optimizer rewrite for bit-accurate equivalence.
func Example() {
	policy := &devpolicy.Policy{Rules: []devpolicy.Rule{
		// Root grants rwm to char majors 1..3.
		{Layer: 0, Order: 0, Effect: devpolicy.EffectAllow, Type: devpolicy.TypeChar,
			MajorMin: 1, MajorMax: 3, MinorMin: 0, MinorMax: devpolicy.MaxDeviceNo,
			Perm: devpolicy.MaskAll, Name: "root"},
		// A child cannot widen access; the intersection keeps it inside root.
		{Layer: 1, Order: 0, Effect: devpolicy.EffectAllow, Type: devpolicy.TypeAll,
			MajorMin: 0, MajorMax: devpolicy.MaxDeviceNo, MinorMin: 0, MinorMax: devpolicy.MaxDeviceNo,
			Perm: devpolicy.MaskAll, Name: "child"},
	}}
	compiled, err := policy.Compile()
	if err != nil {
		panic(err)
	}
	fmt.Printf("block(0,0)=0b%03b\n", compiled.EffectiveMask(devpolicy.TypeBlock, 0, 0))

	// The spec only allows read on the same window, so w and m are escalations.
	spec := &devpolicy.Spec{}
	spec.AddAllow(devpolicy.TypeChar, 1, 3, 0, devpolicy.MaxDeviceNo, devpolicy.BitR, "spec-r")
	specCompiled, err := spec.Compile()
	if err != nil {
		panic(err)
	}
	report, err := devpolicy.Audit(compiled, specCompiled)
	if err != nil {
		panic(err)
	}
	v := report.MinEscalation
	fmt.Printf("escalation=%c(%d,%d)%s\n", v.Type, v.Major, v.Minor, devpolicy.BitName(v.Bit))

	// An mknod-only grant never implies read or write.
	mOnly, err := (&devpolicy.Policy{Rules: []devpolicy.Rule{
		{Layer: 0, Effect: devpolicy.EffectAllow, Type: devpolicy.TypeAll,
			MajorMin: 0, MajorMax: devpolicy.MaxDeviceNo, MinorMin: 0, MinorMax: devpolicy.MaxDeviceNo,
			Perm: devpolicy.BitM, Name: "mknod"},
	}}).Compile()
	if err != nil {
		panic(err)
	}
	fmt.Printf("mknod-only(9,9)=0b%03b\n", mOnly.EffectiveMask(devpolicy.TypeBlock, 9, 9))

	// Optimizer equivalence entry: identical semantics are accepted.
	tiled := &devpolicy.Policy{Rules: []devpolicy.Rule{
		{Layer: 0, Effect: devpolicy.EffectAllow, Type: devpolicy.TypeChar,
			MajorMin: 1, MajorMax: 2, MinorMin: 0, MinorMax: devpolicy.MaxDeviceNo, Perm: devpolicy.BitR, Name: "t1"},
		{Layer: 0, Effect: devpolicy.EffectAllow, Type: devpolicy.TypeChar,
			MajorMin: 3, MajorMax: 3, MinorMin: 0, MinorMax: devpolicy.MaxDeviceNo, Perm: devpolicy.BitR, Name: "t2"},
	}}
	readOnly := &devpolicy.Policy{Rules: []devpolicy.Rule{
		{Layer: 0, Effect: devpolicy.EffectAllow, Type: devpolicy.TypeChar,
			MajorMin: 1, MajorMax: 3, MinorMin: 0, MinorMax: devpolicy.MaxDeviceNo, Perm: devpolicy.BitR, Name: "ro"},
	}}
	ok, _, err := devpolicy.Equivalent(readOnly, tiled)
	if err != nil {
		panic(err)
	}
	fmt.Printf("optimizer-equivalent=%v\n", ok)

	// Output:
	// block(0,0)=0b000
	// escalation=c(1,0)w
	// mknod-only(9,9)=0b100
	// optimizer-equivalent=true
}
