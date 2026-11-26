package gin

import "testing"

func TestKleeneLogic(t *testing.T) {
	cases := []struct {
		name string
		got  Truth
		want Truth
	}{
		{"false and maybe", TriAnd(False, Maybe), False},
		{"true and maybe", TriAnd(True, Maybe), Maybe},
		{"maybe and maybe", TriAnd(Maybe, Maybe), Maybe},
		{"true or maybe", TriOr(True, Maybe), True},
		{"false or maybe", TriOr(False, Maybe), Maybe},
		{"not maybe", TriNot(Maybe), Maybe},
		{"not true", TriNot(True), False},
		{"not false", TriNot(False), True},
		{"empty and", TriAnd(), True},
		{"empty or", TriOr(), False},
	}
	for _, test := range cases {
		if test.got != test.want {
			t.Fatalf("%s: got %s, want %s", test.name, test.got, test.want)
		}
	}
}
