package rar_test

import (
	"encoding/json"
	"errors"
	"testing"

	"example.org/netsec/q0346/rar"
)

const paymentsURL = "https://api.example.com/payments"

func strptr(value string) *string {
	return &value
}

func baseGranted() []rar.Detail {
	return []rar.Detail{
		{
			Type:       "payment_initiation",
			Locations:  []string{paymentsURL},
			Actions:    []string{"create", "read", "list"},
			Datatypes:  []string{"amount", "debtor", "creditor"},
			Identifier: strptr("acct-1"),
		},
		{
			Type:      "account_access",
			Locations: []string{"https://api.example.com/accounts/1", "https://api.example.com/accounts/2"},
			Actions:   []string{"read"},
			Datatypes: []string{"balance", "transactions"},
		},
		{
			Type:      "document_access",
			Locations: []string{"https://docs.example.com/d/1"},
			Actions:   []string{"read"},
			Datatypes: []string{"metadata"},
		},
		{
			Type:      "document_access",
			Locations: []string{"https://docs.example.com/d/1"},
			Actions:   []string{"write"},
			Datatypes: []string{"content"},
		},
	}
}

func TestSatisfyAllowed(t *testing.T) {
	granted := baseGranted()

	tests := []struct {
		name      string
		requested []rar.Detail
	}{
		{
			name: "exact subset with matching identifier",
			requested: []rar.Detail{{
				Type:       "payment_initiation",
				Locations:  []string{paymentsURL},
				Actions:    []string{"create"},
				Datatypes:  []string{"amount"},
				Identifier: strptr("acct-1"),
			}},
		},
		{
			name: "omitted identifier is not constrained",
			requested: []rar.Detail{{
				Type:      "payment_initiation",
				Locations: []string{paymentsURL},
				Actions:   []string{"read"},
				Datatypes: []string{"creditor", "debtor"},
			}},
		},
		{
			name: "array order does not matter",
			requested: []rar.Detail{{
				Type:      "account_access",
				Locations: []string{"https://api.example.com/accounts/2", "https://api.example.com/accounts/1"},
				Actions:   []string{"read"},
				Datatypes: []string{"transactions", "balance"},
			}},
		},
		{
			name: "empty actions and datatypes are vacuously covered",
			requested: []rar.Detail{{
				Type:      "account_access",
				Locations: []string{"https://api.example.com/accounts/1"},
			}},
		},
		{
			name:      "type-only detail is covered by same type",
			requested: []rar.Detail{{Type: "account_access"}},
		},
		{
			name: "one granted detail must satisfy the whole request",
			requested: []rar.Detail{{
				Type:      "document_access",
				Locations: []string{"https://docs.example.com/d/1"},
				Actions:   []string{"read"},
				Datatypes: []string{"metadata"},
			}},
		},
		{
			name:      "empty request is trivially allowed",
			requested: nil,
		},
		{
			name: "repeated identical request items count once",
			requested: []rar.Detail{
				{
					Type:      "account_access",
					Locations: []string{"https://api.example.com/accounts/1"},
					Actions:   []string{"read"},
				},
				{
					Actions:   []string{"read"},
					Locations: []string{"https://api.example.com/accounts/1"},
					Type:      "account_access",
				},
				{
					Type:      "account_access",
					Locations: []string{"https://api.example.com/accounts/1"},
					Actions:   []string{"read"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := rar.Satisfy(tt.requested, granted); err != nil {
				t.Fatalf("Satisfy() unexpected error: %v", err)
			}
			if !rar.Subset(tt.requested, granted) {
				t.Fatalf("Subset() = false, want true")
			}
		})
	}
}

func TestSatisfyRejected(t *testing.T) {
	granted := baseGranted()

	tests := []struct {
		name      string
		requested []rar.Detail
		wantField rar.Field
	}{
		{
			name: "unauthorized URL",
			requested: []rar.Detail{{
				Type:      "account_access",
				Locations: []string{"https://api.example.com/accounts/999"},
				Actions:   []string{"read"},
			}},
			wantField: rar.FieldLocations,
		},
		{
			name: "prefix impersonation with extended path",
			requested: []rar.Detail{{
				Type:      "account_access",
				Locations: []string{"https://api.example.com/accounts/1/transactions"},
				Actions:   []string{"read"},
			}},
			wantField: rar.FieldLocations,
		},
		{
			name: "prefix impersonation without boundary slash",
			requested: []rar.Detail{{
				Type:      "account_access",
				Locations: []string{"https://api.example.com/accounts"},
				Actions:   []string{"read"},
			}},
			wantField: rar.FieldLocations,
		},
		{
			name: "trailing slash is a distinct byte sequence",
			requested: []rar.Detail{{
				Type:       "payment_initiation",
				Locations:  []string{paymentsURL + "/"},
				Actions:    []string{"create"},
				Identifier: strptr("acct-1"),
			}},
			wantField: rar.FieldLocations,
		},
		{
			name: "unknown type alone rejects the request",
			requested: []rar.Detail{{
				Type:      "fund_transfer",
				Locations: []string{paymentsURL},
			}},
			wantField: rar.FieldType,
		},
		{
			name: "unknown type rejects whole request even next to valid item",
			requested: []rar.Detail{
				{
					Type:      "account_access",
					Locations: []string{"https://api.example.com/accounts/1"},
					Actions:   []string{"read"},
				},
				{Type: "fund_transfer"},
			},
			wantField: rar.FieldType,
		},
		{
			name: "action beyond granted superset",
			requested: []rar.Detail{{
				Type:       "payment_initiation",
				Locations:  []string{paymentsURL},
				Actions:    []string{"create", "refund"},
				Identifier: strptr("acct-1"),
			}},
			wantField: rar.FieldActions,
		},
		{
			name: "datatype beyond granted superset",
			requested: []rar.Detail{{
				Type:       "payment_initiation",
				Locations:  []string{paymentsURL},
				Actions:    []string{"read"},
				Datatypes:  []string{"tax_id"},
				Identifier: strptr("acct-1"),
			}},
			wantField: rar.FieldDatatypes,
		},
		{
			name: "identifier mismatch",
			requested: []rar.Detail{{
				Type:       "payment_initiation",
				Locations:  []string{paymentsURL},
				Actions:    []string{"read"},
				Identifier: strptr("acct-2"),
			}},
			wantField: rar.FieldIdentifier,
		},
		{
			name: "requested identifier where grant has none",
			requested: []rar.Detail{{
				Type:       "account_access",
				Locations:  []string{"https://api.example.com/accounts/1"},
				Actions:    []string{"read"},
				Identifier: strptr("acct-1"),
			}},
			wantField: rar.FieldIdentifier,
		},
		{
			name: "matching is existential, grants cannot be combined",
			requested: []rar.Detail{{
				Type:      "document_access",
				Locations: []string{"https://docs.example.com/d/1"},
				Actions:   []string{"read", "write"},
				Datatypes: []string{"metadata", "content"},
			}},
			wantField: rar.FieldActions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rar.Satisfy(tt.requested, granted)
			if err == nil {
				t.Fatalf("Satisfy() = nil, want rejection on %s", tt.wantField)
			}
			violation := rar.AsViolation(err)
			if violation == nil {
				t.Fatalf("error %v is not a *rar.Violation", err)
			}
			if violation.Field != tt.wantField {
				t.Fatalf("violation field = %q, want %q", violation.Field, tt.wantField)
			}
			if !errors.Is(err, err) {
				t.Fatalf("error lost identity")
			}
		})
	}
}

func TestDuplicateGrantsCountedOnce(t *testing.T) {
	grant := rar.Detail{
		Type:      "account_access",
		Locations: []string{"https://api.example.com/accounts/1"},
		Actions:   []string{"read"},
	}
	granted := []rar.Detail{grant, grant, grant}
	requested := []rar.Detail{grant}

	if err := rar.Satisfy(requested, granted); err != nil {
		t.Fatalf("duplicate grants must not break coverage: %v", err)
	}
}

func TestRefreshAllowed(t *testing.T) {
	original := baseGranted()

	t.Run("refresh within original consent", func(t *testing.T) {
		refreshed := []rar.Detail{{
			Type:       "payment_initiation",
			Locations:  []string{paymentsURL},
			Actions:    []string{"read"},
			Datatypes:  []string{"amount"},
			Identifier: strptr("acct-1"),
		}}
		if err := rar.RefreshAllowed(refreshed, original); err != nil {
			t.Fatalf("RefreshAllowed() unexpected error: %v", err)
		}
	})

	expansionTests := []struct {
		name      string
		refreshed []rar.Detail
		wantField rar.Field
	}{
		{
			name: "refresh adds URL",
			refreshed: []rar.Detail{{
				Type:       "payment_initiation",
				Locations:  []string{paymentsURL, "https://api.example.com/payments/stealth"},
				Actions:    []string{"read"},
				Identifier: strptr("acct-1"),
			}},
			wantField: rar.FieldLocations,
		},
		{
			name: "refresh adds action",
			refreshed: []rar.Detail{{
				Type:       "payment_initiation",
				Locations:  []string{paymentsURL},
				Actions:    []string{"read", "cancel"},
				Identifier: strptr("acct-1"),
			}},
			wantField: rar.FieldActions,
		},
		{
			name: "refresh adds datatype",
			refreshed: []rar.Detail{{
				Type:       "payment_initiation",
				Locations:  []string{paymentsURL},
				Actions:    []string{"read"},
				Datatypes:  []string{"amount", "card_number"},
				Identifier: strptr("acct-1"),
			}},
			wantField: rar.FieldDatatypes,
		},
		{
			name:      "refresh adds unknown type",
			refreshed: []rar.Detail{{Type: "admin_sweep"}},
			wantField: rar.FieldType,
		},
		{
			name: "refresh changes identifier",
			refreshed: []rar.Detail{{
				Type:       "payment_initiation",
				Locations:  []string{paymentsURL},
				Actions:    []string{"read"},
				Identifier: strptr("acct-other"),
			}},
			wantField: rar.FieldIdentifier,
		},
	}

	for _, tt := range expansionTests {
		t.Run(tt.name, func(t *testing.T) {
			err := rar.RefreshAllowed(tt.refreshed, original)
			if err == nil {
				t.Fatalf("RefreshAllowed() = nil, want %s rejection", tt.wantField)
			}
			violation := rar.AsViolation(err)
			if violation == nil || violation.Field != tt.wantField {
				t.Fatalf("RefreshAllowed() error = %v, want field %s", err, tt.wantField)
			}
		})
	}
}

func TestParseDetails(t *testing.T) {
	t.Run("omitted and explicit empty identifier stay distinct", func(t *testing.T) {
		data := []byte(`[
			{"type":"account_access","locations":["https://api.example.com/accounts/1"],"actions":["read"]},
			{"type":"payment_initiation","identifier":""},
			{"type":"payment_initiation","identifier":"acct-1"}
		]`)
		details, err := rar.ParseDetails(data)
		if err != nil {
			t.Fatalf("ParseDetails() unexpected error: %v", err)
		}
		if len(details) != 3 {
			t.Fatalf("got %d details, want 3", len(details))
		}
		if details[0].HasIdentifier() {
			t.Fatalf("omitted identifier must stay nil")
		}
		if value, ok := details[1].IdentifierValue(); !ok || value != "" {
			t.Fatalf("explicit empty identifier = (%q,%v), want (\"\",true)", value, ok)
		}
		if value, ok := details[2].IdentifierValue(); !ok || value != "acct-1" {
			t.Fatalf("explicit identifier = (%q,%v), want (\"acct-1\",true)", value, ok)
		}
	})

	invalid := []struct {
		name string
		data string
	}{
		{name: "not an array", data: `{"type":"account_access"}`},
		{name: "missing type", data: `[{"locations":["https://api.example.com/accounts/1"]}]`},
		{name: "non-string type", data: `[{"type":42}]`},
		{name: "non-string location element", data: `[{"type":"account_access","locations":[1]}]`},
		{name: "non-string identifier", data: `[{"type":"account_access","identifier":7}]`},
		{name: "malformed json", data: `[`},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := rar.ParseDetails([]byte(tt.data)); err == nil {
				t.Fatalf("ParseDetails(%s) = nil error, want error", tt.name)
			}
		})
	}
}

func TestJSONRoundTripAndConstructors(t *testing.T) {
	locations := []string{"https://api.example.com/accounts/1"}
	actions := []string{"read"}
	datatypes := []string{"balance"}

	withoutIdentifier := rar.NewDetail("account_access", locations, actions, datatypes)
	if withoutIdentifier.HasIdentifier() {
		t.Fatalf("NewDetail must not set an identifier")
	}

	withIdentifier := rar.NewDetailWithIdentifier("payment_initiation", locations, actions, datatypes, "acct-1")
	value, ok := withIdentifier.IdentifierValue()
	if !ok || value != "acct-1" {
		t.Fatalf("NewDetailWithIdentifier identifier = (%q,%v)", value, ok)
	}

	encoded, err := json.Marshal(withIdentifier)
	if err != nil {
		t.Fatalf("Marshal() unexpected error: %v", err)
	}
	decoded, err := rar.ParseDetails(append([]byte("["), append(encoded, ']')...))
	if err != nil {
		t.Fatalf("ParseDetails() unexpected error: %v", err)
	}
	if !rar.Subset(decoded, []rar.Detail{withIdentifier}) {
		t.Fatalf("round-tripped detail must be covered by its source: %s", encoded)
	}

	var decodedWithout rar.Detail
	if err := json.Unmarshal(mustMarshal(t, withoutIdentifier), &decodedWithout); err != nil {
		t.Fatalf("Unmarshal() unexpected error: %v", err)
	}
	if decodedWithout.HasIdentifier() {
		t.Fatalf("omitted identifier must round-trip as nil")
	}
}

func TestNilViolation(t *testing.T) {
	if violation := rar.AsViolation(nil); violation != nil {
		t.Fatalf("AsViolation(nil) = %v, want nil", violation)
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() unexpected error: %v", err)
	}
	return data
}
