package csvq

import "testing"

func TestQuotedCommasAndDuplicateHeaders(t *testing.T) {
	tab, err := Parse([]byte("id,name,name\n1,\"a,b\",x\n2,\"p,q\",y\n"))
	if err != nil {
		t.Fatal(err)
	}
	if tab.Header.Len() != 3 {
		t.Fatalf("header width = %d, want 3", tab.Header.Len())
	}
	// Duplicate names are kept; identity is positional.
	if tab.Header.Name(1) != "name" || tab.Header.Name(2) != "name" {
		t.Fatalf("duplicate header names not preserved: %+v", tab.Header.Columns)
	}
	if len(tab.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(tab.Records))
	}
	r := tab.Records[0]
	if r.Fields[1].Value != "a,b" || !r.Fields[1].Quoted {
		t.Fatalf("quoted comma field = %+v", r.Fields[1])
	}
	if r.Fields[2].Value != "x" {
		t.Fatalf("positional field 2 = %q, want x", r.Fields[2].Value)
	}
	if tab.Records[1].Fields[1].Value != "p,q" {
		t.Fatalf("second record field 1 = %q", tab.Records[1].Fields[1].Value)
	}
}

func TestShortRecordPaddedMissingVsEmpty(t *testing.T) {
	tab, err := Parse([]byte("a,b,c\n1,,3\n4,5\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(tab.Records))
	}
	empty := tab.Records[0].Fields[1]
	if !empty.Present || !empty.Empty || empty.Value != "" {
		t.Fatalf("empty field = %+v, want present and empty", empty)
	}
	missing := tab.Records[1].Fields[2]
	if missing.Present || missing.Empty {
		t.Fatalf("missing field = %+v, want not present", missing)
	}
	if len(tab.Records[1].Fields) != 3 {
		t.Fatalf("short record not padded to width 3: %+v", tab.Records[1].Fields)
	}
}

func TestLongRecordQuarantinedWholeRow(t *testing.T) {
	tab, err := Parse([]byte("a,b,c\n1,2,3,4\n7,8,9\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Quarantine) != 1 {
		t.Fatalf("quarantined = %d, want 1", len(tab.Quarantine))
	}
	q := tab.Quarantine[0]
	if q.Seq != 0 {
		t.Fatalf("quarantined seq = %d, want 0", q.Seq)
	}
	if len(q.Fields) != 4 {
		t.Fatalf("quarantined row kept %d fields, want the whole row of 4", len(q.Fields))
	}
	if string(q.Raw) != "1,2,3,4\n" {
		t.Fatalf("quarantined raw = %q", q.Raw)
	}
	// The quarantine must not shift later rows' column positions.
	if len(tab.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(tab.Records))
	}
	r := tab.Records[0]
	if r.Seq != 1 || r.Fields[0].Value != "7" || r.Fields[1].Value != "8" || r.Fields[2].Value != "9" {
		t.Fatalf("record after quarantine = seq %d %+v", r.Seq, r.Fields)
	}
}

func TestQuotedNewlineDoesNotTriggerRowRepair(t *testing.T) {
	tab, err := Parse([]byte("a,b\n\"line1\nline2\",x\n\"r1\r\nr2\",y\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Records) != 2 || len(tab.Quarantine) != 0 {
		t.Fatalf("records = %d quarantined = %d, want 2 and 0", len(tab.Records), len(tab.Quarantine))
	}
	if got := tab.Records[0].Fields[0].Value; got != "line1\nline2" {
		t.Fatalf("quoted newline field = %q", got)
	}
	if got := tab.Records[1].Fields[0].Value; got != "r1\r\nr2" {
		t.Fatalf("quoted CRLF field = %q", got)
	}
	// An unterminated quote swallows the rest as one quarantined row;
	// embedded newlines must not split it into repaired rows.
	tab2, err := Parse([]byte("a,b\n\"open\n1,2\n3,4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tab2.Records) != 0 || len(tab2.Quarantine) != 1 {
		t.Fatalf("unterminated quote: records = %d quarantined = %d, want 0 and 1",
			len(tab2.Records), len(tab2.Quarantine))
	}
}

func TestMalformedQuoteQuarantined(t *testing.T) {
	tab, err := Parse([]byte("a,b\n\"bad\"x,2\n3,4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Quarantine) != 1 || len(tab.Records) != 1 {
		t.Fatalf("quarantined = %d records = %d, want 1 and 1", len(tab.Quarantine), len(tab.Records))
	}
	if tab.Records[0].Fields[0].Value != "3" {
		t.Fatalf("record after malformed row = %+v", tab.Records[0].Fields)
	}
}

func TestEscapedQuotes(t *testing.T) {
	tab, err := Parse([]byte("a,b\n\"say \"\"hi\"\"\",z\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := tab.Records[0].Fields[0].Value; got != `say "hi"` {
		t.Fatalf("escaped quote field = %q", got)
	}
}

func TestRecordByteRanges(t *testing.T) {
	src := []byte("a,b\n1,2\n\"x\ny\",3\n")
	tab, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range tab.Records {
		_ = r
	}
	if tab.Records[0].Offset != 4 || tab.Records[0].EndOffset != 8 {
		t.Fatalf("record 0 range = [%d,%d)", tab.Records[0].Offset, tab.Records[0].EndOffset)
	}
	if string(src[tab.Records[1].Offset:tab.Records[1].EndOffset]) != "\"x\ny\",3\n" {
		t.Fatalf("record 1 raw span = %q", src[tab.Records[1].Offset:tab.Records[1].EndOffset])
	}
}
