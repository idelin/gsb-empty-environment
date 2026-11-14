package netsec_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	netsec "example.org/netsec/q0293"
)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func testManager(t *testing.T, clock *fakeClock, ttl time.Duration, singleUse bool) *netsec.CursorManager {
	t.Helper()
	manager, err := netsec.NewCursorManager(netsec.CursorConfig{
		Secret:    []byte("0123456789abcdef0123456789abcdef"),
		TTL:       ttl,
		Now:       clock.Now,
		SingleUse: singleUse,
	})
	if err != nil {
		t.Fatalf("NewCursorManager: %v", err)
	}
	return manager
}

func testScope(tenantID, status string) netsec.Scope {
	return netsec.Scope{
		TenantID: tenantID,
		Query: map[string]any{
			"status": status,
			"order":  []string{"score DESC", "id ASC"},
		},
	}
}

func isZeroPosition(position netsec.Position) bool {
	return len(position.SortKey) == 0 && position.UniqueKey == "" && position.Offset == 0
}

func tokenClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token does not have three parts: %q", token)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims
}

func rewriteClaim(t *testing.T, token, key string, value any) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token does not have three parts")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	claims[key] = value
	tamperedPayload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	// Keep the original signature: this is exactly what an attacker can do.
	return parts[0] + "." + base64.RawURLEncoding.EncodeToString(tamperedPayload) + "." + parts[2]
}

func TestCrossTenantAndTamperedCursorsAreRejected(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	manager := testManager(t, clock, time.Minute, false)

	cursor, err := manager.Encode(testScope("tenant-a", "active"), netsec.Position{
		SortKey:   []string{"10"},
		UniqueKey: "a-3",
		Offset:    3,
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	position, err := manager.Decode(testScope("tenant-b", "active"), cursor)
	if !errors.Is(err, netsec.ErrTenantMismatch) || !errors.Is(err, netsec.ErrScopeMismatch) {
		t.Fatalf("cross-tenant Decode error = %v, want tenant/scope mismatch", err)
	}
	if !isZeroPosition(position) {
		t.Fatalf("cross-tenant Decode leaked position: %+v", position)
	}

	position, ok := manager.ResumeOrStart(testScope("tenant-b", "active"), cursor)
	if ok || !isZeroPosition(position) {
		t.Fatalf("cross-tenant ResumeOrStart = (%+v, %t), want zero/false", position, ok)
	}

	tamperedTenant := rewriteClaim(t, cursor, "t", "tenant-b")
	if _, err := manager.Decode(testScope("tenant-b", "active"), tamperedTenant); !errors.Is(err, netsec.ErrInvalidCursor) {
		t.Fatalf("tampered tenant error = %v, want ErrInvalidCursor", err)
	}

	tamperedOffset := rewriteClaim(t, cursor, "o", float64(999))
	if _, err := manager.Decode(testScope("tenant-a", "active"), tamperedOffset); !errors.Is(err, netsec.ErrInvalidCursor) {
		t.Fatalf("tampered offset error = %v, want ErrInvalidCursor", err)
	}
}

func TestQueryChangeInvalidatesCursor(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	manager := testManager(t, clock, time.Minute, false)
	scope := testScope("tenant-a", "active")

	cursor, err := manager.Encode(scope, netsec.Position{
		SortKey:   []string{"20"},
		UniqueKey: "a-7",
		Offset:    7,
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	position, err := manager.Decode(testScope("tenant-a", "disabled"), cursor)
	if !errors.Is(err, netsec.ErrQueryMismatch) || !errors.Is(err, netsec.ErrScopeMismatch) {
		t.Fatalf("changed-filter Decode error = %v, want query/scope mismatch", err)
	}
	if !isZeroPosition(position) {
		t.Fatalf("changed-filter Decode leaked position: %+v", position)
	}

	position, ok := manager.ResumeOrStart(testScope("tenant-a", "disabled"), cursor)
	if ok || !isZeroPosition(position) {
		t.Fatalf("changed-filter ResumeOrStart = (%+v, %t), want zero/false", position, ok)
	}

	position, err = manager.Decode(scope, cursor)
	if err != nil {
		t.Fatalf("unchanged scope Decode: %v", err)
	}
	if position.UniqueKey != "a-7" || position.Offset != 7 {
		t.Fatalf("Decode = %+v, want a-7 at offset 7", position)
	}
}

func TestCursorExpiration(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	manager := testManager(t, clock, 30*time.Second, false)
	scope := testScope("tenant-a", "active")

	cursor, err := manager.Encode(scope, netsec.Position{
		SortKey:   []string{"10"},
		UniqueKey: "a-2",
		Offset:    2,
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	expiryNumber, ok := tokenClaims(t, cursor)["exp"].(json.Number)
	if !ok {
		t.Fatalf("exp claim is not a JSON number")
	}
	expiry, err := expiryNumber.Int64()
	if err != nil || expiry != clock.now.Add(30*time.Second).UnixNano() {
		t.Fatalf("exp claim = %v, want %d", expiryNumber, clock.now.Add(30*time.Second).UnixNano())
	}

	clock.Advance(29 * time.Second)
	if _, err := manager.Decode(scope, cursor); err != nil {
		t.Fatalf("Decode before expiry: %v", err)
	}

	clock.Advance(time.Second)
	position, err := manager.Decode(scope, cursor)
	if !errors.Is(err, netsec.ErrExpiredCursor) {
		t.Fatalf("Decode at expiry error = %v, want ErrExpiredCursor", err)
	}
	if !isZeroPosition(position) {
		t.Fatalf("expired Decode leaked position: %+v", position)
	}
}

func TestSingleUseCursorCannotBeReplayed(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	manager := testManager(t, clock, time.Minute, true)
	scope := testScope("tenant-a", "active")

	cursor, err := manager.Encode(scope, netsec.Position{
		SortKey:   []string{"8"},
		UniqueKey: "a-9",
		Offset:    9,
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := manager.Decode(scope, cursor); err != nil {
		t.Fatalf("first Decode: %v", err)
	}
	position, err := manager.Decode(scope, cursor)
	if !errors.Is(err, netsec.ErrReplayedCursor) {
		t.Fatalf("second Decode error = %v, want ErrReplayedCursor", err)
	}
	if !isZeroPosition(position) {
		t.Fatalf("replayed Decode leaked position: %+v", position)
	}
}

func TestUniqueTieBreakerIsRequired(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	manager := testManager(t, clock, time.Minute, false)

	if _, err := manager.Encode(testScope("tenant-a", "active"), netsec.Position{
		SortKey: []string{"10"},
		Offset:  1,
	}); !errors.Is(err, netsec.ErrInvalidPosition) {
		t.Fatalf("Encode without tie-breaker error = %v, want ErrInvalidPosition", err)
	}
}

type listRow struct {
	Tenant string
	ID     string
	Score  int
	Status string
}

func listRowsAfter(rows []listRow, tenantID, status string, after *netsec.Position, limit int) ([]listRow, error) {
	var candidates []listRow
	for _, row := range rows {
		if row.Tenant != tenantID || row.Status != status {
			continue
		}
		if after != nil {
			if len(after.SortKey) != 1 {
				return nil, fmt.Errorf("unexpected sort key length %d", len(after.SortKey))
			}
			lastScore, err := strconv.Atoi(after.SortKey[0])
			if err != nil {
				return nil, err
			}
			if row.Score > lastScore {
				continue
			}
			if row.Score == lastScore && row.ID <= after.UniqueKey {
				continue
			}
		}
		candidates = append(candidates, row)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].ID < candidates[j].ID
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

func TestLegalPaginationUsesOnlyCurrentTenantAndUniqueTieBreaker(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	manager := testManager(t, clock, 5*time.Minute, false)
	scope := testScope("tenant-a", "active")

	rows := []listRow{
		{Tenant: "tenant-a", ID: "a-1", Score: 12, Status: "active"},
		{Tenant: "tenant-b", ID: "b-1", Score: 11, Status: "active"},
		{Tenant: "tenant-a", ID: "a-2", Score: 10, Status: "active"},
		{Tenant: "tenant-b", ID: "b-2", Score: 10, Status: "active"},
		{Tenant: "tenant-a", ID: "a-3", Score: 10, Status: "active"},
		{Tenant: "tenant-a", ID: "a-4", Score: 10, Status: "active"},
		{Tenant: "tenant-b", ID: "b-3", Score: 9, Status: "active"},
		{Tenant: "tenant-a", ID: "a-5", Score: 8, Status: "active"},
		{Tenant: "tenant-a", ID: "a-6", Score: 7, Status: "active"},
		{Tenant: "tenant-a", ID: "a-7", Score: 99, Status: "disabled"},
	}

	var cursor string
	var seen []string
	var positions []netsec.Position
	for page := 0; page < 10; page++ {
		var after *netsec.Position
		if cursor != "" {
			position, err := manager.Decode(scope, cursor)
			if err != nil {
				t.Fatalf("Decode page %d cursor: %v", page, err)
			}
			if position.Offset != int64(len(seen)) {
				t.Fatalf("page %d offset = %d, want %d", page, position.Offset, len(seen))
			}
			after = &position
			positions = append(positions, position)
		}

		pageRows, err := listRowsAfter(rows, scope.TenantID, "active", after, 2)
		if err != nil {
			t.Fatalf("list page %d: %v", page, err)
		}
		if len(pageRows) == 0 {
			break
		}
		for _, row := range pageRows {
			if row.Tenant != "tenant-a" {
				t.Fatalf("page %d leaked row from %q: %+v", page, row.Tenant, row)
			}
			seen = append(seen, row.ID)
		}

		last := pageRows[len(pageRows)-1]
		cursor, err = manager.Encode(scope, netsec.Position{
			SortKey:   []string{strconv.Itoa(last.Score)},
			UniqueKey: last.ID,
			Offset:    int64(len(seen)),
		})
		if err != nil {
			t.Fatalf("Encode page %d cursor: %v", page, err)
		}
	}

	want := []string{"a-1", "a-2", "a-3", "a-4", "a-5", "a-6"}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("paginated IDs = %v, want %v", seen, want)
	}

	// The equal-score rows a-2, a-3, and a-4 straddle page boundaries. Their
	// unique tie-breakers must still move the cursor exactly once each.
	counts := make(map[string]int)
	for _, id := range seen {
		counts[id]++
	}
	for _, id := range want {
		if counts[id] != 1 {
			t.Fatalf("row %s returned %d times, want exactly once", id, counts[id])
		}
	}
	if len(positions) != 3 {
		t.Fatalf("resumed positions = %d, want 3", len(positions))
	}
}
