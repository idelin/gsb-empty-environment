// admission-demo drives the transactional plugin engine against a Pod-like
// object and prints the transaction outcome. It demonstrates three scenarios:
// normal convergence, a re-run that skips unaffected plugins, and an
// intentionally oscillating plugin whose transaction is rolled back.
package main

import (
	"encoding/json"
	"fmt"

	"example.org/offlinecli/pkg/jsonpatch"
	"example.org/offlinecli/pkg/patchtx"
)

func mustParse(input string) any {
	var doc any
	if err := json.Unmarshal([]byte(input), &doc); err != nil {
		panic(err)
	}
	return doc
}

func mustOp(kind, path string, value any) jsonpatch.Op {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	msg := json.RawMessage(raw)
	return jsonpatch.Op{Op: kind, Path: path, Value: &msg}
}

// sidecarPlugin injects a sidecar once, depending only on /spec/containers.
type sidecarPlugin struct{ calls int }

func (p *sidecarPlugin) Name() string   { return "sidecar-injector" }
func (p *sidecarPlugin) Deps() []string { return []string{"/spec/containers"} }
func (p *sidecarPlugin) Patch(view any) ([]jsonpatch.Op, error) {
	p.calls++
	containers := view.(map[string]any)["spec"].(map[string]any)["containers"].([]any)
	for _, container := range containers {
		if container.(map[string]any)["name"] == "logger" {
			return nil, nil // already injected
		}
	}
	return []jsonpatch.Op{
		mustOp("add", "/spec/containers/-", map[string]any{
			"name":  "logger",
			"image": "registry.example/logger:1.0",
		}),
	}, nil
}

// labelPlugin derives a label from the number of containers.
type labelPlugin struct{ calls int }

func (p *labelPlugin) Name() string   { return "container-count-labeler" }
func (p *labelPlugin) Deps() []string { return []string{"/spec/containers"} }
func (p *labelPlugin) Patch(view any) ([]jsonpatch.Op, error) {
	p.calls++
	containers := view.(map[string]any)["spec"].(map[string]any)["containers"].([]any)
	return []jsonpatch.Op{
		mustOp("add", "/metadata/labels/sidecar-pods", fmt.Sprintf("%d", len(containers))),
	}, nil
}

// flipPlugin oscillates an annotation forever, so the engine must give up with
// a cycle witness instead of spinning.
type flipPlugin struct{}

func (flipPlugin) Name() string   { return "annotation-flipper" }
func (flipPlugin) Deps() []string { return []string{"/metadata/annotations/mood"} }
func (flipPlugin) Patch(view any) ([]jsonpatch.Op, error) {
	annotations := view.(map[string]any)["metadata"].(map[string]any)["annotations"].(map[string]any)
	next := "calm"
	if annotations["mood"] == "calm" {
		next = "stormy"
	}
	return []jsonpatch.Op{mustOp("replace", "/metadata/annotations/mood", next)}, nil
}

func printJSON(label string, value any) {
	raw, _ := json.MarshalIndent(value, "", "  ")
	fmt.Printf("%s:\n%s\n\n", label, raw)
}

func main() {
	pod := mustParse(`{
		"metadata": {"name": "orders-api", "labels": {}, "annotations": {"mood": "calm"}},
		"spec": {"containers": [{"name": "app", "image": "registry.example/app:2.3"}]}
	}`)

	fmt.Println("== scenario 1: converging transaction ==")
	sidecar := &sidecarPlugin{}
	labeler := &labelPlugin{}
	engine := patchtx.NewEngine([]patchtx.Plugin{sidecar, labeler}, 0)
	result, err := engine.Run(pod)
	if err != nil {
		panic(err)
	}
	printJSON("published object", result.Object)
	trace, _ := json.MarshalIndent(result.Rounds, "", "  ")
	fmt.Printf("rounds:\n%s\n", trace)
	fmt.Printf("plugin invocations: sidecar=%d labeler=%d\n\n", sidecar.calls, labeler.calls)

	fmt.Println("== scenario 2: re-run with an unrelated change only ==")
	// Reuse the previously published object with a harmless label tweak so the
	// /spec/containers dependency is unchanged between runs.
	secondInput := result.Object.(map[string]any)
	meta := secondInput["metadata"].(map[string]any)
	meta["labels"].(map[string]any)["retry"] = "1"
	result2, err := engine.Run(secondInput)
	if err != nil {
		panic(err)
	}
	fmt.Printf("invocations on re-run: %d (sidecar=%d labeler=%d)\n\n",
		result2.Invocations, sidecar.calls, labeler.calls)

	fmt.Println("== scenario 3: non-converging plugin rolls back ==")
	original := mustParse(`{
		"metadata": {"name": "orders-api", "labels": {}, "annotations": {"mood": "calm"}},
		"spec": {"containers": [{"name": "app", "image": "registry.example/app:2.3"}]}
	}`)
	before, _ := json.Marshal(original)
	oscillating := patchtx.NewEngine([]patchtx.Plugin{flipPlugin{}, sidecar}, 0)
	_, err = oscillating.Run(original)
	if ncErr, ok := patchtx.AsNonConvergence(err); ok {
		fmt.Printf("non-convergence: %s\n", ncErr.Error())
		witness, _ := json.MarshalIndent(ncErr.Cycle, "", "  ")
		fmt.Printf("cycle witness:\n%s\n", witness)
	} else {
		panic(fmt.Sprintf("expected non-convergence, got %v", err))
	}
	after, _ := json.Marshal(original)
	fmt.Printf("\noriginal object preserved: %v\n", string(before) == string(after))
}
