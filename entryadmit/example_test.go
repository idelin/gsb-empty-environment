package entryadmit_test

import (
	"fmt"

	"example.org/netsec/q0841/entryadmit"
)

func ExampleChecker() {
	checker, err := entryadmit.New("example.com/foo", "v1.2.3")
	if err != nil {
		panic(err)
	}
	fmt.Println("prefix:", checker.Prefix())

	entries := []string{
		"example.com/foo@v1.2.3/go.mod",
		"example.com/foo@v1.2.3/../escape",
	}
	for _, name := range entries {
		if err := checker.Admit(name); err != nil {
			fmt.Println("rejected:", name)
			break
		}
	}
	// Output:
	// prefix: example.com/foo@v1.2.3/
	// rejected: example.com/foo@v1.2.3/../escape
}

func ExampleCheck() {
	names := []string{
		"example.com/foo@v1.2.3/go.mod",
		"example.com/foo@v1.2.3/README.md",
		"example.com/foo@v1.2.3/readme.md",
	}
	err := entryadmit.Check("example.com/foo", "v1.2.3", names)
	fmt.Println(err != nil)
	// Output:
	// true
}
