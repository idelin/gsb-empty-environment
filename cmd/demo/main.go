// Command demo simulates a small incremental build and prints the shortest
// reason chain for every rebuilt node.
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"incbuild/planner"
)

func write(root, name, content string) {
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		panic(err)
	}
	if err := ioutil.WriteFile(p, []byte(content), 0644); err != nil {
		panic(err)
	}
}

func compile(name, out string, srcs ...string) *planner.Task {
	deps := make([]planner.Dep, 0, len(srcs))
	for _, s := range srcs {
		deps = append(deps, planner.ContentDep(s))
	}
	return &planner.Task{
		Name:    name,
		Deps:    deps,
		Outputs: []string{out},
		Run: func(ctx *planner.RunContext) error {
			var buf []byte
			for _, s := range srcs {
				data, err := ctx.ReadFile(s)
				if err != nil {
					return err
				}
				buf = append(buf, data...)
			}
			return ctx.WriteFile(out, append([]byte(name+":"), buf...))
		},
	}
}

func main() {
	root, err := ioutil.TempDir("", "incbuild-demo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)

	write(root, "main.c", "main-v1\n")
	write(root, "util.c", "util-v1\n")

	p := planner.New(root)
	must(p.AddTask(compile("main.o", "main.o", "main.c")))
	must(p.AddTask(compile("util.o", "util.o", "util.c")))
	must(p.AddTask(&planner.Task{
		Name:    "link",
		Deps:    []planner.Dep{planner.ContentDep("main.o"), planner.ContentDep("util.o")},
		Outputs: []string{"app"},
		Run: func(ctx *planner.RunContext) error {
			// Discovered at run time: the linker script.
			ctx.DependContent("app.ld")
			var buf []byte
			for _, o := range []string{"main.o", "util.o", "app.ld"} {
				data, err := ctx.ReadFile(o)
				if os.IsNotExist(err) {
					continue // placeholder attempt; will be discarded
				}
				if err != nil {
					return err
				}
				buf = append(buf, data...)
			}
			return ctx.WriteFile("app", buf)
		},
	}))
	must(p.AddTask(&planner.Task{
		Name:    "gen-ld",
		Outputs: []string{"app.ld"},
		Run: func(ctx *planner.RunContext) error {
			return ctx.WriteFile("app.ld", []byte("SECTIONS{}\n"))
		},
	}))

	fmt.Println("== build 1 (cold) ==")
	res, err := p.Build()
	must(err)
	fmt.Print(res.Report())

	fmt.Println("== build 2 (no changes) ==")
	res, err = p.Build()
	must(err)
	fmt.Print(res.Report())

	fmt.Println("== build 3 (util.c edited) ==")
	write(root, "util.c", "util-v2\n")
	res, err = p.Build()
	must(err)
	fmt.Print(res.Report())

	fmt.Println("== build 4 (generated main.o deleted) ==")
	if err := os.Remove(filepath.Join(root, "main.o")); err != nil {
		panic(err)
	}
	res, err = p.Build()
	must(err)
	fmt.Print(res.Report())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
