// Command incbuild-demo demonstrates the incremental build planner on a
// diamond-shaped graph with an order-only dependency.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"incbuild"
)

func concatTask(name string, inputs, orderOnly, outputs []string, transform func(string) string) *incbuild.Task {
	return &incbuild.Task{
		Name:      name,
		Cmd:       "concat:" + name,
		Inputs:    inputs,
		OrderOnly: orderOnly,
		Outputs:   outputs,
		Run: func(rc *incbuild.RunContext) error {
			var data string
			for _, f := range inputs {
				b, err := os.ReadFile(filepath.Join(rc.Dir, f))
				if err != nil {
					return err
				}
				data += string(b)
			}
			return os.WriteFile(filepath.Join(rc.Dir, outputs[0]), []byte(transform(data)), 0o644)
		},
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func main() {
	dir, err := os.MkdirTemp("", "incbuild-demo")
	must(err)
	defer os.RemoveAll(dir)

	must(os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a1"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("m1"), 0o644))

	storePath := filepath.Join(dir, ".incbuild.json")
	build := func() *incbuild.BuildResult {
		store, err := incbuild.LoadStore(storePath)
		must(err)
		eng := &incbuild.Engine{Dir: dir, Store: store, Tasks: []*incbuild.Task{
			concatTask("gen-b", []string{"a.txt"}, nil, []string{"b.txt"}, func(s string) string { return s + "B" }),
			concatTask("gen-c", []string{"a.txt"}, nil, []string{"c.txt"}, func(s string) string { return s + "C" }),
			concatTask("gen-d", []string{"b.txt", "c.txt"}, []string{"marker.txt"}, []string{"d.txt"}, func(s string) string { return s }),
		}}
		r, err := eng.Build()
		must(err)
		must(store.Save(storePath))
		return r
	}

	fmt.Println("== build 1: cold ==")
	fmt.Print(build().Report())

	fmt.Println("== build 2: no changes ==")
	fmt.Print(build().Report())

	must(os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a2"), 0o644))
	fmt.Println("== build 3: a.txt modified (diamond propagates) ==")
	fmt.Print(build().Report())

	must(os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("m2"), 0o644))
	fmt.Println("== build 4: order-only marker modified (no propagation) ==")
	fmt.Print(build().Report())
}
