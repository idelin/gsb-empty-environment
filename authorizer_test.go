package netsec_test

import (
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"example.org/netsec/q0669"
)

type memoryStore map[string]*netsec.Article

func (s memoryStore) load(messageID string) (*netsec.Article, bool) {
	art, ok := s[messageID]
	return art, ok
}

func article(messageID, xref string) *netsec.Article {
	headers := map[string][]string{"Message-ID": {messageID}}
	if xref != "" {
		headers["Xref"] = []string{xref}
	}
	return &netsec.Article{
		MessageID: messageID,
		Headers:   headers,
		Body:      []byte("body of " + messageID + "\n"),
	}
}

func TestLookupArticleMessageIDAuthorization(t *testing.T) {
	store := memoryStore{
		"<restricted-only@example>": article("<restricted-only@example>", "news.example secret.group:41"),
		"<crosspost@example>":       article("<crosspost@example>", "news.example secret.group:42 public.group:17"),
		"<public-only@example>":     article("<public-only@example>", "news.example public.group:18"),
	}
	auth := netsec.NewAuthorizer([]string{"public.group"})

	got, err := auth.LookupArticle("<public-only@example>", store.load)
	if err != nil {
		t.Fatalf("public article denied: %v", err)
	}
	if got == nil || got.MessageID != "<public-only@example>" {
		t.Fatalf("wrong article returned: %#v", got)
	}

	got, err = auth.LookupArticle("<crosspost@example>", store.load)
	if err != nil {
		t.Fatalf("crossposted article denied: %v", err)
	}
	if xref := got.Xref(); xref != "news.example public.group:17" {
		t.Fatalf("restricted group leaked in Xref: %q", xref)
	}

	for _, messageID := range []string{"<restricted-only@example>", "<missing@example>"} {
		got, err = auth.LookupArticle(messageID, store.load)
		if got != nil {
			t.Fatalf("%s returned content", messageID)
		}
		if !errors.Is(err, netsec.ErrNoSuchArticle) {
			t.Fatalf("%s: got %v, want ErrNoSuchArticle", messageID, err)
		}
		if err.Error() != netsec.ReplyNoSuchArticle {
			t.Fatalf("unexpected denial text: %q", err.Error())
		}
	}
}

func TestDenialReplyIndistinguishable(t *testing.T) {
	store := memoryStore{
		"<restricted-only@example>": article("<restricted-only@example>", "news.example secret.group:41"),
	}
	auth := netsec.NewAuthorizer([]string{"public.group"})

	_, unauthorized := auth.LookupArticle("<restricted-only@example>", store.load)
	_, missing := auth.LookupArticle("<missing@example>", store.load)

	if unauthorized != missing {
		t.Fatalf("denial sentinels differ: unauthorized=%v missing=%v", unauthorized, missing)
	}
	if unauthorized.Error() != missing.Error() {
		t.Fatalf("denial text differs: %q vs %q", unauthorized, missing)
	}
	if len(unauthorized.Error()) != len(missing.Error()) {
		t.Fatalf("denial length differs: %d vs %d", len(unauthorized.Error()), len(missing.Error()))
	}
	if netsec.NoSuchArticleResponse() != "430 No such article\r\n" {
		t.Fatalf("unexpected wire reply: %q", netsec.NoSuchArticleResponse())
	}
}

func TestDenialTimingComparable(t *testing.T) {
	auth := netsec.NewAuthorizer([]string{"public.group"})
	missing := memoryStore{}
	unreadable := memoryStore{
		"<restricted-only@example>": article("<restricted-only@example>", "news.example secret.group:41"),
	}

	const (
		batches  = 9
		perBatch = 2000
	)
	measure := func(store memoryStore, messageID string) time.Duration {
		start := time.Now()
		for i := 0; i < perBatch; i++ {
			_, _ = auth.LookupArticle(messageID, store.load)
		}
		return time.Since(start)
	}

	// Warm up allocation and map-access paths before measuring.
	measure(missing, "<missing@example>")
	measure(unreadable, "<restricted-only@example>")

	var missingSamples, unreadableSamples []time.Duration
	for i := 0; i < batches; i++ {
		// Alternate batch order to avoid CPU-frequency effects favouring the
		// path that always runs first.
		if i%2 == 0 {
			missingSamples = append(missingSamples, measure(missing, "<missing@example>"))
			unreadableSamples = append(unreadableSamples, measure(unreadable, "<restricted-only@example>"))
		} else {
			unreadableSamples = append(unreadableSamples, measure(unreadable, "<restricted-only@example>"))
			missingSamples = append(missingSamples, measure(missing, "<missing@example>"))
		}
	}
	missingTime := medianDuration(missingSamples)
	unreadableTime := medianDuration(unreadableSamples)

	// The two paths do the same authorization and filtering work. A generous
	// bound keeps this deterministic on noisy CI machines while still catching
	// an early-return side channel.
	if maxDuration(missingTime, unreadableTime) > 5*minDuration(missingTime, unreadableTime) {
		t.Fatalf("denial timing differs: missing=%s unreadable=%s", missingTime, unreadableTime)
	}
}

func medianDuration(samples []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func TestFilterXrefAndReturnedHeader(t *testing.T) {
	auth := netsec.NewAuthorizer([]string{"public.group"})

	got := auth.FilterXref("news.example secret.group:1 public.group:2 secret.other:3")
	if got != "news.example public.group:2" {
		t.Fatalf("FilterXref = %q", got)
	}
	if got := auth.FilterXref("news.example secret.group:1"); got != "" {
		t.Fatalf("fully restricted Xref survived: %q", got)
	}

	art := article("<x@example>", "news.example secret.group:1 public.group:2")
	art.Headers["XREF"] = []string{"news.example secret.group:3"}
	filtered := auth.FilterArticle(art)
	if xref := filtered.Xref(); xref != "news.example public.group:2" {
		t.Fatalf("returned Xref = %q", xref)
	}
	if _, ok := filtered.Headers["XREF"]; ok {
		t.Fatal("unreadable case-variant Xref key survived")
	}
	if xref := art.Xref(); xref == "" {
		t.Fatal("FilterArticle mutated its input")
	}

	allRestricted := article("<r@example>", "news.example secret.group:1")
	filtered = auth.FilterArticle(allRestricted)
	if _, ok := filtered.Headers["Xref"]; ok {
		t.Fatal("Xref header should be omitted when no readable group remains")
	}
}

func TestFoldedAndServerlessXref(t *testing.T) {
	auth := netsec.NewAuthorizer([]string{"comp.lang.go"})

	parsed := netsec.ParseXref("Xref: news.example\r\n\talt.secret:1 comp.lang.go:7")
	if parsed.Server != "news.example" || len(parsed.Entries) != 2 {
		t.Fatalf("bad folded parse: %#v", parsed)
	}
	if got := auth.FilterXref("alt.secret:1 comp.lang.go:7"); got != "comp.lang.go:7" {
		t.Fatalf("serverless Xref filter = %q", got)
	}
}

func TestWildmatAndPermissionPatterns(t *testing.T) {
	cases := []struct {
		pattern string
		group   string
		want    bool
	}{
		{"comp.*", "comp.lang.go", true},
		{"comp.*", "alt.comp", false},
		{"comp.?.go", "comp.a.go", true},
		{"comp.?.go", "comp.ab.go", false},
		{"comp.[a-c].go", "comp.b.go", true},
		{"comp.[^a-c].go", "comp.d.go", true},
		{"*,!comp.*", "alt.test", true},
		{"*,!comp.*", "comp.lang.go", false},
	}
	for _, tc := range cases {
		if got := netsec.MatchGroup(tc.pattern, tc.group); got != tc.want {
			t.Errorf("MatchGroup(%q, %q) = %v, want %v", tc.pattern, tc.group, got, tc.want)
		}
	}

	auth := netsec.NewAuthorizer([]string{"public.*"})
	if !auth.CanReadGroup("public.a") || auth.CanReadGroup("secret.a") {
		t.Fatal("wildcard read permission not applied")
	}
}

func TestHDRAndXPATFiltering(t *testing.T) {
	auth := netsec.NewAuthorizer([]string{"public.group"})
	results := []netsec.HeaderResult{
		{Group: "public.group", Number: 1, Field: "Subject", Value: "visible"},
		{Group: "secret.group", Number: 2, Field: "Subject", Value: "hidden"},
		{Group: "", MessageID: "<crosspost@example>", Xref: "news.example secret.group:3 public.group:4", Field: "Subject", Value: "crossposted"},
		{Group: "", MessageID: "<restricted@example>", Xref: "news.example secret.group:5", Field: "Subject", Value: "restricted"},
	}

	if got := auth.FilterHDR("secret.group", results); len(got) != 0 {
		t.Fatalf("HDR on restricted group returned %d rows", len(got))
	}
	if got := auth.FilterHDR("", results); len(got) != 1 || got[0].Value != "crossposted" {
		t.Fatalf("message-id HDR results = %#v", got)
	}
	got := auth.FilterHDR("public.group", results)
	if len(got) != 2 {
		t.Fatalf("group HDR rows = %#v", got)
	}
	for _, row := range got {
		if row.Group == "secret.group" || row.Value == "restricted" {
			t.Fatalf("restricted HDR row survived: %#v", row)
		}
	}
	if got := auth.FilterXPAT("secret.group", results); len(got) != 0 {
		t.Fatalf("XPAT on restricted group returned %d rows", len(got))
	}
	if got := auth.FilterXPAT("", results); len(got) != 1 || got[0].Value != "crossposted" {
		t.Fatalf("message-id XPAT results = %#v", got)
	}

	xrefResults := []netsec.HeaderResult{
		{Group: "public.group", Number: 9, Field: "Xref", Value: "news.example secret.group:1 public.group:9"},
	}
	got = auth.FilterHDR("public.group", xrefResults)
	if len(got) != 1 || got[0].Value != "news.example public.group:9" {
		t.Fatalf("HDR Xref value was not filtered: %#v", got)
	}

	valueOnlyXref := []netsec.HeaderResult{
		{MessageID: "<crosspost@example>", Field: "Xref", Value: "news.example secret.group:1 public.group:9"},
	}
	got = auth.FilterHDR("", valueOnlyXref)
	if len(got) != 1 || got[0].Value != "news.example public.group:9" {
		t.Fatalf("message-id HDR Xref value was not authorized/filtered: %#v", got)
	}
}

func TestNEWNEWSWildcardFiltersGroupsFirst(t *testing.T) {
	available := []string{"public.a", "public.b", "secret.a", "secret.b"}
	auth := netsec.NewAuthorizer([]string{"public.*"})

	expanded := auth.ExpandGroups("*", available)
	want := []string{"public.a", "public.b"}
	if !reflect.DeepEqual(expanded, want) {
		t.Fatalf("ExpandGroups(*) = %#v, want %#v", expanded, want)
	}
	if expanded := auth.ExpandGroups("secret.*", available); len(expanded) != 0 {
		t.Fatalf("restricted wildcard was expanded: %#v", expanded)
	}

	refs := []netsec.ArticleRef{
		{Group: "public.a", Number: 1, MessageID: "<visible@example>", Xref: "news.example public.a:1"},
		{Group: "secret.a", Number: 2, MessageID: "<hidden@example>", Xref: "news.example secret.a:2"},
		{Group: "", MessageID: "<crosspost@example>", Xref: "news.example secret.a:3 public.b:8"},
		{Group: "", MessageID: "<crosspost-hidden@example>", Xref: "news.example secret.a:4 secret.b:9"},
		{Group: "secret.a", MessageID: "<spoofed@example>", Xref: "news.example public.a:5"},
	}

	got := auth.FilterNEWNEWS("*", available, refs)
	if len(got) != 2 {
		t.Fatalf("NEWNEWS(*) returned %#v", got)
	}
	if got[0].MessageID != "<visible@example>" || got[1].MessageID != "<crosspost@example>" {
		t.Fatalf("NEWNEWS(*) returned %#v", got)
	}

	if got := auth.FilterNEWNEWS("secret.*", available, refs); len(got) != 0 {
		t.Fatalf("NEWNEWS(secret.*) returned %#v", got)
	}
	if got := auth.FilterNewNews("public.b", available, refs); len(got) != 1 || got[0].MessageID != "<crosspost@example>" {
		t.Fatalf("NEWNEWS(public.b) returned %#v", got)
	}
	if got := auth.FilterNEWNEWS("*", nil, refs); len(got) != 2 {
		t.Fatalf("NEWNEWS(*) without a group list returned %#v", got)
	}
}

func TestFilterXrefDoesNotAffectAllowedGroups(t *testing.T) {
	auth := netsec.NewAuthorizer([]string{"public.a", "public.b"})
	xref := "news.example public.a:1 public.b:2"
	if got := auth.FilterXref(xref); got != xref {
		t.Fatalf("allowed Xref changed: %q -> %q", xref, got)
	}
}

func BenchmarkLookupMissing(b *testing.B) {
	auth := netsec.NewAuthorizer([]string{"public.group"})
	store := memoryStore{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = auth.LookupArticle("<missing@example>", store.load)
	}
}

func BenchmarkLookupUnreadable(b *testing.B) {
	auth := netsec.NewAuthorizer([]string{"public.group"})
	store := memoryStore{
		"<restricted-only@example>": article("<restricted-only@example>", "news.example secret.group:41"),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = auth.LookupArticle("<restricted-only@example>", store.load)
	}
}
