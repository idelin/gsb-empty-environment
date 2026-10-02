package logtmpl

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		text string
		want TokenType
	}{
		{"200", TokNumber},
		{"3.14", TokNumber},
		{"ERR-503", TokErrorCode},
		{"error_42", TokErrorCode},
		{"E1234", TokErrorCode},
		{"DB-1042", TokErrorCode},
		{"a1b2c3d4e5f6", TokID},
		{"550e8400-e29b-41d4-a716-446655440000", TokID},
		{"worker-1", TokWord}, // single digit: not an error code
		{"user-01", TokWord},  // lowercase prefix: not an error code
		{"alice", TokWord},
	}
	for _, c := range cases {
		if got := Classify(c.text); got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestErrFamily(t *testing.T) {
	cases := map[string]string{
		"ERR-503":  "ERR",
		"error_42": "ERR",
		"E900":     "E",
		"DB-1042":  "DB",
	}
	for in, want := range cases {
		if got := ErrFamily(in); got != want {
			t.Errorf("ErrFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTokenizePunct(t *testing.T) {
	toks := Tokenize("status=200 ok")
	want := []Token{
		{TokWord, "status"},
		{TokPunct, "="},
		{TokNumber, "200"},
		{TokWord, "ok"},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d: %v", len(toks), len(want), toks)
	}
	for i, w := range want {
		if toks[i] != w {
			t.Errorf("token %d = %+v, want %+v", i, toks[i], w)
		}
	}
}
