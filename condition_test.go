package netsec

import (
	"reflect"
	"testing"
)

func TestConditionEvaluation(t *testing.T) {
	condition := And(
		Flag("linux"),
		Or(
			NotFlag("arm64"),
			Flag("ci"),
		),
	)

	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{name: "linux amd64", cfg: Config{"linux": true}, want: true},
		{name: "linux arm64", cfg: Config{"linux": true, "arm64": true}, want: false},
		{name: "linux arm64 ci", cfg: Config{"linux": true, "arm64": true, "ci": true}, want: true},
		{name: "darwin", cfg: Config{"linux": false, "ci": true}, want: false},
		{name: "missing flags are false", cfg: Config{"linux": true}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := condition.Eval(test.cfg); got != test.want {
				t.Fatalf("Eval(%s) = %t, want %t", test.cfg, got, test.want)
			}
		})
	}

	if got, want := condition.Variables(), []string{"arm64", "ci", "linux"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Variables() = %#v, want %#v", got, want)
	}
}

func TestConditionConstructors(t *testing.T) {
	if !Always().Eval(Config{}) {
		t.Fatal("Always must be true")
	}
	if Never().Eval(Config{}) {
		t.Fatal("Never must be false")
	}
	if !Not(Never()).Eval(Config{}) {
		t.Fatal("Not(Never) must be true")
	}
	if !And().Eval(Config{}) {
		t.Fatal("empty And must be true")
	}
	if Any().Eval(Config{}) {
		t.Fatal("empty Any must be false")
	}
	if !Or(Flag("a"), Flag("b")).Eval(Config{"b": true}) {
		t.Fatal("Or must accept either flag")
	}
	if !When("a", false).Eval(Config{}) {
		t.Fatal("When(a, false) must be true when a is false")
	}
}
