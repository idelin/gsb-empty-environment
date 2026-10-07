package resolver_test

import (
	"fmt"

	"example.com/depresolve/resolver"
)

func ExampleSolve() {
	m := &resolver.Manifest{
		Versions: map[string][]resolver.Version{
			"app":  {{1, 0, 0}, {2, 0, 0}},
			"lib":  {{1, 0, 0}, {2, 0, 0}},
			"util": {{1, 0, 0}},
		},
		Deps: map[resolver.PkgVersion][]resolver.Dependency{
			{Pkg: "app", Ver: resolver.Version{2, 0, 0}}: {
				{Pkg: "lib", Range: resolver.Range{
					Lo: resolver.Version{2, 0, 0}, Hi: resolver.Version{2, 0, 0}}},
			},
			{Pkg: "app", Ver: resolver.Version{1, 0, 0}}: {
				{Pkg: "lib", Range: resolver.AnyRange()},
			},
			{Pkg: "lib", Ver: resolver.Version{1, 0, 0}}: {
				{Pkg: "util", Range: resolver.AnyRange()},
			},
		},
	}
	lock, err := resolver.Solve(m, []resolver.Requirement{
		{Pkg: "app", Range: resolver.AnyRange()},
	})
	if err != nil {
		fmt.Println("no solution:", err)
		return
	}
	fmt.Println(lock)
	// Output: app@2.0.0, lib@2.0.0
}
