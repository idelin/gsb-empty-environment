package validate

import (
	"strings"
	"testing"
)

type inner struct {
	Code string `validate:"required,len=3"`
}

type sample struct {
	ID      string   `validate:"required"`
	Weight  int      `validate:"min=1,max=64"`
	Region  string   `validate:"required,min=2,max=8"`
	Tags    []string `validate:"max=3"`
	In      inner
	Members []inner
}

func validSample() sample {
	return sample{
		ID:      "node-1",
		Weight:  4,
		Region:  "cn-east",
		Tags:    []string{"a"},
		In:      inner{Code: "abc"},
		Members: []inner{{Code: "xyz"}},
	}
}

func TestValid(t *testing.T) {
	if err := Validate(validSample()); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if err := Validate(&sample{ID: "x", Weight: 1, Region: "cn", In: inner{Code: "abc"}}); err != nil {
		t.Fatalf("pointer: expected valid, got %v", err)
	}
}

func TestRequired(t *testing.T) {
	s := validSample()
	s.ID = ""
	err := Validate(s)
	if err == nil || !strings.Contains(err.Error(), "ID") || !strings.Contains(err.Error(), "required") {
		t.Fatalf("want required violation on ID, got %v", err)
	}
}

func TestMinMaxNumeric(t *testing.T) {
	s := validSample()
	s.Weight = 0
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "min=1") {
		t.Fatalf("want min violation, got %v", err)
	}
	s.Weight = 65
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "max=64") {
		t.Fatalf("want max violation, got %v", err)
	}
}

func TestMinMaxStringLength(t *testing.T) {
	s := validSample()
	s.Region = "x"
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "Region") {
		t.Fatalf("want min-length violation on Region, got %v", err)
	}
}

func TestSliceMaxLen(t *testing.T) {
	s := validSample()
	s.Tags = []string{"a", "b", "c", "d"}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "Tags") {
		t.Fatalf("want max violation on Tags, got %v", err)
	}
}

func TestNestedPaths(t *testing.T) {
	s := validSample()
	s.In.Code = ""
	s.Members = append(s.Members, inner{Code: "toolong"})
	err := Validate(s)
	if err == nil {
		t.Fatal("want violations")
	}
	vs, ok := err.(Violations)
	if !ok {
		t.Fatalf("want Violations, got %T", err)
	}
	// In.Code="" breaks both required and len=3; Members[1].Code breaks len=3.
	if len(vs) != 3 {
		t.Fatalf("want 3 violations, got %v", vs)
	}
	if !strings.Contains(vs[0].Path, "In.Code") {
		t.Fatalf("want path In.Code, got %s", vs[0].Path)
	}
	if !strings.Contains(vs[2].Path, "Members[1].Code") || !strings.Contains(vs[2].Rule, "len=3") {
		t.Fatalf("want path Members[1].Code len=3, got %+v", vs[2])
	}
}

func TestAggregateErrorMessage(t *testing.T) {
	s := validSample()
	s.ID = ""
	s.Weight = 99
	err := Validate(s)
	vs, ok := err.(Violations)
	if !ok || len(vs) != 2 {
		t.Fatalf("want 2 violations, got %v", err)
	}
	if !strings.Contains(err.Error(), "2 validation violations") {
		t.Fatalf("aggregate message: %v", err)
	}
}
