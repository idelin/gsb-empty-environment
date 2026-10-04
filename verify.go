package main

import (
	"fmt"
	"reflect"
)

// runOnce executes the program in a single uninterrupted run.
func runOnce(prog *Program, budget int64) Result {
	vm := NewVM(prog, budget)
	vm.Run()
	return vm.Result()
}

// runCheckpointed executes k instructions, serializes the VM, restores it
// into a fresh VM, and runs to completion.
func runCheckpointed(prog *Program, budget, k int64) (Result, error) {
	vm := NewVM(prog, budget)
	for i := int64(0); i < k && !vm.Done(); i++ {
		vm.Step()
	}
	data, err := vm.Snapshot()
	if err != nil {
		return Result{}, err
	}
	vm2, err := RestoreVM(prog, data)
	if err != nil {
		return Result{}, err
	}
	vm2.Run()
	return vm2.Result(), nil
}

// runChainedCheckpoints pauses and restores at each of the given step
// counts (in increasing order), then runs to completion.
func runChainedCheckpoints(prog *Program, budget int64, ks []int64) (Result, error) {
	vm := NewVM(prog, budget)
	var done int64
	for _, k := range ks {
		for done < k && !vm.Done() {
			vm.Step()
			done++
		}
		data, err := vm.Snapshot()
		if err != nil {
			return Result{}, err
		}
		vm, err = RestoreVM(prog, data)
		if err != nil {
			return Result{}, err
		}
	}
	vm.Run()
	return vm.Result(), nil
}

// Verify compares a single uninterrupted run against checkpoint/resume runs
// at every possible breakpoint (and a chained multi-checkpoint run),
// checking output, first error, and total gas. It returns a list of
// mismatch descriptions; empty means fully consistent.
func Verify(prog *Program, budget int64) []string {
	base := runOnce(prog, budget)
	var problems []string

	// Every executed instruction costs exactly 1 gas, so base.Gas is also
	// the number of executed steps; try a breakpoint at every one of them.
	for k := int64(0); k <= base.Gas; k++ {
		got, err := runCheckpointed(prog, budget, k)
		if err != nil {
			problems = append(problems, fmt.Sprintf("checkpoint %d: %v", k, err))
			continue
		}
		if !reflect.DeepEqual(base, got) {
			problems = append(problems, fmt.Sprintf(
				"checkpoint %d: single-run %+v != resumed %+v", k, base, got))
		}
	}

	// Chained checkpoints at quartiles.
	q := base.Gas / 4
	chain, err := runChainedCheckpoints(prog, budget, []int64{q, 2 * q, 3 * q})
	if err != nil {
		problems = append(problems, fmt.Sprintf("chained: %v", err))
	} else if !reflect.DeepEqual(base, chain) {
		problems = append(problems, fmt.Sprintf(
			"chained checkpoints: single-run %+v != resumed %+v", base, chain))
	}
	return problems
}
