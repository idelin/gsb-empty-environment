package errmerge

import (
	"reflect"
	"strings"
	"testing"
)

func isErr(o Output) bool { return strings.HasPrefix(string(o), "Err") }

func tr(next State, out Output) Transition { return Transition{Next: next, Out: out} }

// authMachine models a login service in which the secret is whether the
// account exists. Each secret state rejects with its own error category,
// and the second attempt rejects with yet another pair of categories.
func authMachine() *Machine {
	return &Machine{
		States: []State{"idle", "user", "ghost", "user2", "ghost2"},
		Inputs: []Input{"try", "reset"},
		Trans: map[State]map[Input]Transition{
			"idle":   {"try": tr("idle", "OK"), "reset": tr("idle", "OK")},
			"user":   {"try": tr("user2", "ErrBadPassword"), "reset": tr("idle", "OK")},
			"ghost":  {"try": tr("ghost2", "ErrNoUser"), "reset": tr("idle", "OK")},
			"user2":  {"try": tr("user2", "ErrLocked"), "reset": tr("idle", "OK")},
			"ghost2": {"try": tr("ghost2", "ErrThrottle"), "reset": tr("idle", "OK")},
		},
	}
}

var secretGroup = [][]State{{"user", "ghost"}}

func TestPartitionMergesOnlyWhatIsNeeded(t *testing.T) {
	res, err := Partition(authMachine(), secretGroup, isErr)
	if err != nil {
		t.Fatal(err)
	}
	if res.Witness != nil {
		t.Fatalf("unexpected witness: %s", res.Witness)
	}
	want := [][]Output{
		{"ErrBadPassword", "ErrNoUser"},
		{"ErrLocked", "ErrThrottle"},
	}
	if !reflect.DeepEqual(res.Classes, want) {
		t.Fatalf("classes = %v, want %v", res.Classes, want)
	}
	if _, ok := res.Merged["OK"]; ok {
		t.Fatalf("success output OK must not be merged: %v", res.Merged)
	}
}

func TestCheckBeforeAndAfterMerge(t *testing.T) {
	m := authMachine()
	leak, err := Check(m, secretGroup, nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	if leak == nil {
		t.Fatal("pre-merge machine should leak")
	}
	if !reflect.DeepEqual(leak.Inputs, []Input{"try"}) || leak.Position != 0 {
		t.Fatalf("pre-merge leak = %s", leak)
	}

	res, err := Partition(m, secretGroup, isErr)
	if err != nil {
		t.Fatal(err)
	}
	leak, err = Check(m, secretGroup, res.Merged, 4)
	if err != nil {
		t.Fatal(err)
	}
	if leak != nil {
		t.Fatalf("post-merge machine still leaks: %s", leak)
	}
}

func TestWitnessWhenSuccessDiverges(t *testing.T) {
	m := authMachine()
	m.Trans["user"]["try"] = tr("user2", "OKLogin")
	res, err := Partition(m, secretGroup, isErr)
	if err != nil {
		t.Fatal(err)
	}
	if res.Witness == nil {
		t.Fatal("expected a witness, got classes")
	}
	w := res.Witness
	if len(w.Prefix) != 0 || w.Input != "try" {
		t.Fatalf("witness = %s", w)
	}
	if w.Outputs != [2]Output{"OKLogin", "ErrNoUser"} {
		t.Fatalf("witness outputs = %v", w.Outputs)
	}
}

func TestWitnessAtDepthTwo(t *testing.T) {
	m := &Machine{
		States: []State{"s1", "s2", "t1", "t2"},
		Inputs: []Input{"a"},
		Trans: map[State]map[Input]Transition{
			"s1": {"a": tr("t1", "ErrA")},
			"s2": {"a": tr("t2", "ErrB")},
			"t1": {"a": tr("t1", "OKDeep")},
			"t2": {"a": tr("t2", "ErrC")},
		},
	}
	res, err := Partition(m, [][]State{{"s1", "s2"}}, isErr)
	if err != nil {
		t.Fatal(err)
	}
	if res.Witness == nil {
		t.Fatal("expected a witness, got classes")
	}
	w := res.Witness
	if !reflect.DeepEqual(w.Prefix, []Input{"a"}) || w.Input != "a" {
		t.Fatalf("witness = %s", w)
	}
	if w.States != [2]State{"t1", "t2"} || w.Outputs != [2]Output{"OKDeep", "ErrC"} {
		t.Fatalf("witness = %s", w)
	}
}

func TestTransitiveMergeAcrossThreeSecrets(t *testing.T) {
	m := &Machine{
		States: []State{"s1", "s2", "s3"},
		Inputs: []Input{"a"},
		Trans: map[State]map[Input]Transition{
			"s1": {"a": tr("s1", "ErrOne")},
			"s2": {"a": tr("s2", "ErrTwo")},
			"s3": {"a": tr("s3", "ErrThree")},
		},
	}
	res, err := Partition(m, [][]State{{"s1", "s2", "s3"}}, isErr)
	if err != nil {
		t.Fatal(err)
	}
	if res.Witness != nil {
		t.Fatalf("unexpected witness: %s", res.Witness)
	}
	want := [][]Output{{"ErrOne", "ErrThree", "ErrTwo"}}
	if !reflect.DeepEqual(res.Classes, want) {
		t.Fatalf("classes = %v, want %v", res.Classes, want)
	}
	leak, err := Check(m, [][]State{{"s1", "s2", "s3"}}, res.Merged, 3)
	if err != nil {
		t.Fatal(err)
	}
	if leak != nil {
		t.Fatalf("post-merge machine still leaks: %s", leak)
	}
}

func TestValidateRejectsPartialMachine(t *testing.T) {
	m := &Machine{
		States: []State{"s1"},
		Inputs: []Input{"a", "b"},
		Trans:  map[State]map[Input]Transition{"s1": {"a": tr("s1", "OK")}},
	}
	if _, err := Partition(m, [][]State{{"s1"}}, isErr); err == nil {
		t.Fatal("expected validation error for missing transition")
	}
}
