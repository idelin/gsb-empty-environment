package ageplugin

import (
	"bytes"
	"errors"
	"testing"
)

func TestAliasSessionWrapsAndValidates(t *testing.T) {
	greet := "greet v1\n\n"
	response := "recipient-stanza AGE-PLUGIN-TEST-1 0\n" +
		"c29tZS13cmFwLWtleQ==\n" +
		"\n" +
		"done\n\n"

	session, err := NewSession(bytes.NewBufferString(greet+response), &bytes.Buffer{}, "AGE-PLUGIN-TEST-1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.BinaryName() != "age-plugin-test" {
		t.Fatalf("BinaryName = %q", session.BinaryName())
	}

	results, err := session.WrapFileKey(make([]byte, FileKeySize))
	if err != nil {
		t.Fatalf("WrapFileKey: %v", err)
	}
	if len(results) != 1 || string(results[0].Body) != "some-wrap-key" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestAliasSentinelsAndLimits(t *testing.T) {
	if MaxPluginResponse != 64*1024 {
		t.Fatalf("MaxPluginResponse = %d", MaxPluginResponse)
	}
	if _, err := ParsePluginName("AGE-PLUGIN-../evil-1"); !errors.Is(err, ErrInvalidPluginName) {
		t.Fatalf("error = %v, want ErrInvalidPluginName", err)
	}
}
