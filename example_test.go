package netsec

import "fmt"

func ExampleBuilder() {
	b := NewBuilder()
	if err := b.SetBudget(Budget{MaxOutputBytes: 4096, MaxDepth: 8}); err != nil {
		fmt.Println("budget error:", err)
		return
	}

	// Inherited variables are untrusted and never re-parsed.
	if err := b.Import(
		[]string{"USER=root", "EVIL=${USER}/x"},
		[]string{"USER", "EVIL"},
	); err != nil {
		fmt.Println("import error:", err)
		return
	}

	mustDefineExample(b, Spec{Name: "HOME", Template: "/home/${USER}"})
	mustDefineExample(b, Spec{Name: "PATH", Template: "${HOME}/bin:${EVIL}"})
	mustDefineExample(b, Spec{Name: "TOKEN", Template: "rotated-secret", Sensitive: true})

	env, err := b.Build()
	if err != nil {
		fmt.Println("build error:", err)
		return
	}

	for _, line := range env.Env() {
		fmt.Println(line)
	}

	home, _ := env.Get("HOME")
	fmt.Printf("inherited USER byte origin: %s\n",
		home.OriginAt(len("/home/")).Source)

	// Output:
	// USER=root
	// EVIL=${USER}/x
	// HOME=/home/root
	// PATH=/home/root/bin:${USER}/x
	// TOKEN=rotated-secret
	// inherited USER byte origin: untrusted
}

func mustDefineExample(b *Builder, spec Spec) {
	if err := b.Define(spec); err != nil {
		panic(err)
	}
}
