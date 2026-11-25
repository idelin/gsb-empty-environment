package spectest

import (
	"fmt"
	"sort"
	"testing"
	"time"

	. "prescreen.local/ps-c7499fbc67"
)

var testBase = time.Unix(1_700_000_000, 0).UTC()

func tt(minutes int) time.Time {
	return testBase.Add(time.Duration(minutes) * time.Minute)
}

func testOrder(t *testing.T, minDomains int, from, until int) Order {
	t.Helper()
	o, err := NewOrder("8.8.8.8", 443, ChallengeHTTP01, "account-1",
		"nonce-1", DigestBytes([]byte("public-key")), minDomains, tt(from), tt(until))
	if err != nil {
		t.Fatalf("NewOrder: %v", err)
	}
	return o
}

func mkAttr(id, probe, domain, origin string, from, until time.Time) Attribution {
	return Attribution{
		ID:          id,
		ProbeID:     probe,
		Domain:      domain,
		RouteOrigin: origin,
		ValidFrom:   from,
		ValidUntil:  until,
		Proof:       DigestBytes([]byte("identity:" + id)),
	}
}

func mkList(version string, attrs ...Attribution) IdentityList {
	list := IdentityList{Version: version, Entries: attrs}
	list.Digest = list.ContentDigest()
	return list
}

func mkObserved(t *testing.T, segments ...[2]int) TimeSpan {
	t.Helper()
	parts := make([]Interval, len(segments))
	for i, s := range segments {
		parts[i] = Interval{Start: tt(s[0]), End: tt(s[1])}
	}
	span, err := NewTimeSpan(parts...)
	if err != nil {
		t.Fatalf("NewTimeSpan: %v", err)
	}
	return span
}

type witnessSpec struct {
	id       string
	probe    string
	version  string
	attrID   string
	domain   string
	origin   string
	observed TimeSpan
	cost     int64
}

func mkTestWitness(spec witnessSpec, order Order) Witness {
	return Witness{
		ID:              spec.id,
		ProbeID:         spec.probe,
		IdentityVersion: spec.version,
		AttributionID:   spec.attrID,
		Domain:          spec.domain,
		RouteOrigin:     spec.origin,
		Observed:        spec.observed,
		ConnectedAddr:   order.AddrPort(),
		ProofDigest:     DigestBytes([]byte("proof:" + spec.id)),
		OrderDigest:     order.Digest(),
		Cost:            spec.cost,
	}
}

func evidenceIDs(b Bundle) []string {
	ids := make([]string, len(b.Evidence))
	for i, ev := range b.Evidence {
		ids[i] = ev.Witness.ID
	}
	sort.Strings(ids)
	return ids
}

func mustAggregate(t *testing.T, req Request) Bundle {
	t.Helper()
	b, err := Aggregate(req)
	if err != nil {
		t.Fatalf("Aggregate: %v (bundle: %s)", err, summarizeBundle(b))
	}
	return b
}

func summarizeBundle(b Bundle) string {
	return fmt.Sprintf("evidence=%v forks=%v rejections=%v", evidenceIDs(b), b.Forks, b.Rejections)
}

func idsString(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id
	}
	return out
}

func requireIDs(t *testing.T, b Bundle, want ...string) {
	t.Helper()
	got := evidenceIDs(b)
	sort.Strings(want)
	if idsString(got) != idsString(want) {
		t.Fatalf("evidence ids = [%s], want [%s]", idsString(got), idsString(want))
	}
}

func requireAccept(t *testing.T, req Request, b Bundle) {
	t.Helper()
	a := Audit(req, b)
	if !a.Valid {
		t.Fatalf("Audit rejected bundle: %v", a.Err())
	}
}
