package netsec

import "testing"

// FuzzCheck exercises the scanner with arbitrary bytes. The guard must
// never panic, must never report success for a document it cannot fully
// classify, and must never hand back an entity table on a failed check.
func FuzzCheck(f *testing.F) {
	seeds := []string{
		"",
		"<root/>",
		"<?xml version=\"1.0\"?><root/>",
		"<!-- c --><root/>",
		"<!DOCTYPE root><root/>",
		"<!DOCTYPE root [<!ENTITY a \"x\">]><root>&a;</root>",
		"<!DOCTYPE root [<!ENTITY a \"&b;\"><!ENTITY b \"&a;\">]><root/>",
		"<!DOCTYPE root [<!ENTITY x SYSTEM \"file:///x\">]><root/>",
		"<!DOCTYPE root [<!ENTITY % p \"x\">]><root/>",
		"<!DOCTYPE root [<!ENTITY a \"&#xD800;\">]><root/>",
		"<![CDATA[",
		"<!DOCTYPE root [<!ELEMENT r EMPTY>]><r/>",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		guard := New(32, nil)
		err := guard.Check([]byte(input))
		if err != nil {
			if got := guard.Entities(); got != nil {
				t.Fatalf("entity table leaked after error: %v", got)
			}
			return
		}
		// A successful check means either no DOCTYPE or a fully validated
		// one; re-running must be deterministic and side-effect free.
		if err := guard.Check([]byte(input)); err != nil {
			t.Fatalf("non-deterministic check: %v", err)
		}
	})
}

func TestFuzzSeedCorpus(t *testing.T) {
	docs := []string{
		"<!DOCTYPE root [<!ELEMENT r EMPTY>]><r/>",
		"<!DOCTYPE root [<!ATTLIST r a CDATA \"v\">]><r/>",
		"<!DOCTYPE root [<!ELEMENT r (#PCDATA)> <!-- c -->]><r/>",
		"<?xml version=\"1.0\"?><!-- prolog comment --><!DOCTYPE r [<!ENTITY a \"v\">]><r/>",
	}
	for idx, doc := range docs {
		if err := New(1<<20, nil).Check([]byte(doc)); err != nil {
			t.Fatalf("seed %d rejected: %v", idx, err)
		}
	}

	bad := []string{
		"<!DOCTYPE root [<!ELEMENT r SYSTEM \"x\">]><r/>",
		"<!DOCTYPE root [<!ENTITY a \"unterminated>]><r/>",
		"<!DOCTYPE root [<!ENTITY \"noname\">]><r/>",
		"<!DOCTYPE root [<!ENTITY a \"x\"]><r/>",
		"<!-- never closed",
		"<?pi never closed",
		"<!DOCTYPE root [<!ENTITY a \"x\">] ",
	}
	for idx, doc := range bad {
		if err := New(1<<20, nil).Check([]byte(doc)); err == nil {
			t.Fatalf("malformed seed %d accepted", idx)
		}
	}
}
