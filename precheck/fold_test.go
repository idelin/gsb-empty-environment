package precheck

import (
	"strings"
	"testing"
)

func TestNormalizeSegmentTrailingTrim(t *testing.T) {
	tab := testTable()
	got, issues := tab.NormalizeSegment("data .")
	if got != "data" {
		t.Errorf("got %q, want %q", got, "data")
	}
	if len(issues) != 1 || issues[0].Kind != IssueTrailingTrim {
		t.Errorf("issues = %v, want one trailing-trim", issues)
	}
}

func TestNormalizeSegmentReservedStem(t *testing.T) {
	tab := testTable()
	for _, seg := range []string{"CON", "con.txt", "Com1.ini"} {
		if _, issues := tab.NormalizeSegment(seg); len(issues) == 0 {
			t.Errorf("segment %q: expected reserved-name violation", seg)
		}
	}
	if _, issues := tab.NormalizeSegment("content.txt"); len(issues) != 0 {
		t.Errorf("content.txt falsely flagged: %v", issues)
	}
}

func TestFoldLongestMatch(t *testing.T) {
	tab := NewFoldTable(map[string]string{"ab": "x", "a": "y"}, nil, "")
	if got := tab.foldString("abab"); got != "xx" {
		t.Errorf("foldString(abab) = %q, want xx", got)
	}
	if got := tab.foldString("aba"); got != "xy" {
		t.Errorf("foldString(aba) = %q, want xy", got)
	}
}

func TestReservedNamesAreFoldedAtLoad(t *testing.T) {
	// Reserved name given in lowercase must still match an uppercase segment
	// because both sides pass through the same table.
	tab := NewFoldTable(asciiCaseMappings(), []string{"nul"}, "")
	if _, issues := tab.NormalizeSegment("NUL"); len(issues) == 0 {
		t.Error("NUL should be reserved via folded table entry")
	}
}

func TestParseListKeepsTrailingSpace(t *testing.T) {
	entries, err := ParseList(strings.NewReader("F notes \nD Dir\n# comment\n\nf a/b.txt\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	if entries[0].Path != "notes " || entries[0].Type != File {
		t.Errorf("entry 0 = %+v, want {notes  file}", entries[0])
	}
	if entries[1].Path != "Dir" || entries[1].Type != Dir {
		t.Errorf("entry 1 = %+v, want {Dir dir}", entries[1])
	}
	if entries[2].Path != "a/b.txt" || entries[2].Type != File {
		t.Errorf("entry 2 = %+v", entries[2])
	}
}
