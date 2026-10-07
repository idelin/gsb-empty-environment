package resolve_test

import (
	"fmt"

	"pkgresolve"
)

func ExampleSolve() {
	m := &resolve.Manifest{
		Packages: map[string]*resolve.Package{
			"app": {
				Name: "app",
				Versions: []resolve.PackageVersion{
					{Version: resolve.Version{Major: 1}},
					{Version: resolve.Version{Major: 2},
						Deps: []resolve.Dep{{Name: "lib", Min: resolve.Version{Major: 1}, Max: resolve.Version{Major: 2}}}},
				},
			},
			"lib": {
				Name: "lib",
				Versions: []resolve.PackageVersion{
					{Version: resolve.Version{Major: 1}},
					{Version: resolve.Version{Major: 2}},
				},
			},
		},
	}
	res, err := resolve.Solve(m, []string{"app"})
	if err != nil {
		panic(err)
	}
	fmt.Print(res.String())
	// Output:
	// app 2.0.0
	// lib 2.0.0
}
