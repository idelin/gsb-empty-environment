package envguard_test

import (
	"fmt"

	"prescreen.local/ps-d8798344b3/envguard"
)

func Example() {
	g := envguard.NewGraph()

	// Untrusted tenant input, constrained to a host-declared character
	// language and tagged with its trust domain.
	_ = g.Bind(envguard.Input{
		Tenant:  "billing",
		Domain:  "guest",
		Name:    "mode",
		Value:   "long",
		Lang:    envguard.MustCompileRegex(`short|long`),
		Trusted: false,
	})

	// A trusted deletion sanitizer: exact relation declared up front.
	_ = g.DeclareSanitizer(envguard.NewRemoveSanitizer(
		"strip-slash",
		[]envguard.RuneRange{{Lo: '/', Hi: '/'}},
	))

	short, _ := g.Literal("abc")
	long, _ := g.Literal("abcdef")
	chosen, _ := g.Cond("billing", "mode", long, short)

	pub := envguard.NewPublisher(g)
	_, err := pub.Build([]envguard.Export{
		{Name: "MODE", Value: chosen},
	})
	re, ok := err.(*envguard.RejectError)
	fmt.Println("rejected:", ok, re.Violation.Kind)

	// Equal branches erase the selector dependency and publish cleanly.
	constant, _ := g.Literal("abc")
	washed, _ := g.Cond("billing", "mode", constant, constant)
	env, err := pub.Build([]envguard.Export{{Name: "MODE", Value: washed}})
	if err != nil {
		panic(err)
	}
	v, _ := env.Get("MODE")
	fmt.Println("published:", v)

	// Output:
	// rejected: true control-origin
	// published: abc
}
