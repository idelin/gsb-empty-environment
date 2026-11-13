package netsec_test

import (
	"context"
	"fmt"

	netsec "example.org/netsec/q0085"
)

func ExampleHost_Run() {
	digest, _ := netsec.ParseDigest("example-package")
	policy := netsec.NewPolicy()
	_ = policy.Grant(digest, netsec.Read("src/input"), netsec.Write("bin/app"))

	files := netsec.NewVirtualFS()
	_ = files.AddSourceFile(digest, "src/input", []byte("hello"))
	host, _ := netsec.NewHost(netsec.HostConfig{Policy: policy, Files: files})

	graph := netsec.NewPackageGraph(netsec.PackageNode{
		Digest: digest,
		Scripts: []netsec.ScriptDeclaration{{
			Name:    "build",
			Actions: []netsec.Action{netsec.Read("src/input"), netsec.Write("bin/app")},
		}},
	})
	plan, _ := host.Plan(graph)
	result, _ := host.Run(context.Background(), plan, netsec.Runners{
		{Package: digest, Name: "build"}: func(_ context.Context, execution *netsec.Execution) error {
			data, err := execution.Read("src/input")
			if err != nil {
				return err
			}
			return execution.Write("bin/app", data)
		},
	})

	fmt.Println(result.Packages[digest].Status)
	fmt.Println(string(result.Published["bin/app"].Data))
	// Output:
	// succeeded
	// hello
}
