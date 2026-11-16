package netsec

import (
	"errors"
	"strings"
	"testing"
)

func testGate(t *testing.T) *Gate {
	t.Helper()
	gate, err := New(Config{
		AllowedMethods:      []string{"eth_call", "eth_getLogs", "eth_blockNumber"},
		MaxBatchSize:        2,
		MaxGetLogsBlockSpan: 100,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return gate
}

func TestValidateAllowsSafeRequests(t *testing.T) {
	gate := testGate(t)

	tests := []struct {
		name string
		body string
	}{
		{
			name: "single request",
			body: `{"jsonrpc":"2.0","method":"eth_call","params":[{},"latest"],"id":1}`,
		},
		{
			name: "batch",
			body: `[
				{"jsonrpc":"2.0","method":"eth_call","params":[],"id":1},
				{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":2}
			]`,
		},
		{
			name: "notification",
			body: `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[]}`,
		},
		{
			name: "getLogs at exact span limit",
			body: `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x1","toBlock":"0x65"}],"id":1}`,
		},
		{
			name: "getLogs same tag",
			body: `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"latest","toBlock":"latest"}],"id":1}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := gate.Validate([]byte(tt.body)); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestValidateRejectsDangerousMethods(t *testing.T) {
	gate := testGate(t)

	tests := []struct {
		name string
		body string
	}{
		{
			name: "personal unlock mixed into batch",
			body: `[
				{"jsonrpc":"2.0","method":"eth_call","params":[],"id":1},
				{"jsonrpc":"2.0","method":"personal_unlockAccount","params":["0xabc","pw",0],"id":2}
			]`,
		},
		{
			name: "debug mixed into batch",
			body: `[
				{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1},
				{"jsonrpc":"2.0","method":"debug_traceCall","params":[{},"latest"],"id":2}
			]`,
		},
		{
			name: "dangerous method wins over invalid sibling params",
			body: `[
				{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x1","toBlock":"0x1000"}],"id":1},
				{"jsonrpc":"2.0","method":"personal_unlockAccount","params":[],"id":2}
			]`,
		},
		{
			name: "case variant method value",
			body: `{"jsonrpc":"2.0","method":"ETH_CALL","params":[],"id":1}`,
		},
		{
			name: "case variant dangerous method value",
			body: `{"jsonrpc":"2.0","method":"Personal_UnlockAccount","params":[],"id":1}`,
		},
		{
			name: "wildcard in allow list is literal",
			body: `{"jsonrpc":"2.0","method":"eth_unknown","params":[],"id":1}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gate.Validate([]byte(tt.body))
			if !errors.Is(err, ErrMethodNotAllowed) {
				t.Fatalf("Validate() error = %v, want ErrMethodNotAllowed", err)
			}
		})
	}
}

func TestValidateRejectsDuplicateKeys(t *testing.T) {
	gate := testGate(t)

	tests := []struct {
		name string
		body string
	}{
		{
			name: "duplicate method",
			body: `{"jsonrpc":"2.0","method":"eth_call","method":"personal_unlockAccount","params":[],"id":1}`,
		},
		{
			name: "escaped duplicate method",
			body: `{"jsonrpc":"2.0","method":"eth_call","meth\u006fd":"personal_unlockAccount","params":[],"id":1}`,
		},
		{
			name: "duplicate nested key",
			body: `{"jsonrpc":"2.0","method":"eth_call","params":[{"to":"0x1","to":"0x2"}],"id":1}`,
		},
		{
			name: "duplicate in batch element",
			body: `[
				{"jsonrpc":"2.0","method":"eth_call","params":[],"id":1},
				{"jsonrpc":"2.0","method":"eth_call","method":"eth_blockNumber","params":[],"id":2}
			]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gate.Validate([]byte(tt.body))
			if !errors.Is(err, ErrDuplicateKey) {
				t.Fatalf("Validate() error = %v, want ErrDuplicateKey", err)
			}
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Validate() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestValidateRejectsCaseVariantKeys(t *testing.T) {
	gate := testGate(t)
	body := `{"jsonrpc":"2.0","Method":"personal_unlockAccount","params":[],"id":1}`

	err := gate.Validate([]byte(body))
	if !errors.Is(err, ErrCaseVariantKey) {
		t.Fatalf("Validate() error = %v, want ErrCaseVariantKey", err)
	}
}

func TestValidateRejectsTooLargeBatch(t *testing.T) {
	gate := testGate(t)
	body := `[
		{"jsonrpc":"2.0","method":"eth_call","params":[],"id":1},
		{"jsonrpc":"2.0","method":"eth_call","params":[],"id":2},
		{"jsonrpc":"2.0","method":"eth_call","params":[],"id":3}
	]`

	err := gate.Validate([]byte(body))
	if !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("Validate() error = %v, want ErrBatchTooLarge", err)
	}
}

func TestValidateRejectsGetLogsOverspan(t *testing.T) {
	gate := testGate(t)

	tests := []struct {
		name string
		body string
	}{
		{
			name: "one block over limit",
			body: `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x1","toBlock":"0x66"}],"id":1}`,
		},
		{
			name: "unbounded latest",
			body: `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x1","toBlock":"latest"}],"id":1}`,
		},
		{
			name: "reversed range",
			body: `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x10","toBlock":"0x1"}],"id":1}`,
		},
		{
			name: "case variant filter key",
			body: `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"FromBlock":"0x1","toBlock":"0x2"}],"id":1}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gate.Validate([]byte(tt.body))
			if err == nil {
				t.Fatal("Validate() error = nil, want rejection")
			}
		})
	}

	if err := gate.Validate([]byte(`{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x1","toBlock":"0x66"}],"id":1}`)); !errors.Is(err, ErrBlockRangeTooLarge) {
		t.Fatalf("Validate() error = %v, want ErrBlockRangeTooLarge", err)
	}
	if err := gate.Validate([]byte(`{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x1","toBlock":"latest"}],"id":1}`)); !errors.Is(err, ErrUnboundedBlockRange) {
		t.Fatalf("Validate() error = %v, want ErrUnboundedBlockRange", err)
	}

	batch := `[
		{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1},
		{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x1","toBlock":"0x66"}],"id":2}
	]`
	if err := gate.Validate([]byte(batch)); !errors.Is(err, ErrBlockRangeTooLarge) {
		t.Fatalf("Validate(batch) error = %v, want ErrBlockRangeTooLarge", err)
	}
}

func TestValidateRejectsMalformedRequests(t *testing.T) {
	gate := testGate(t)

	tests := []string{
		``,
		`[]`,
		`null`,
		`"eth_call"`,
		`{"method":"eth_call","params":[],"id":1}`,
		`{"jsonrpc":"1.0","method":"eth_call","params":[],"id":1}`,
		`{"jsonrpc":"2.0","method":1,"params":[],"id":1}`,
		`{"jsonrpc":"2.0","method":"eth_call","params":null,"id":1}`,
		`{"jsonrpc":"2.0","method":"eth_call","params":[],"id":true}`,
		`{"jsonrpc":"2.0","method":"eth_call","params":[],"id":1} {}`,
		`{"jsonrpc":"2.0","method":"eth_call","params":[],"id":1`,
	}

	for _, body := range tests {
		t.Run(body, func(t *testing.T) {
			if err := gate.Validate([]byte(body)); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Validate(%q) error = %v, want ErrInvalidRequest", body, err)
			}
		})
	}
}

func TestDefaultGateBlocksDangerousMethods(t *testing.T) {
	if err := Validate([]byte(`{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}`)); err != nil {
		t.Fatalf("Validate() safe method error = %v", err)
	}
	if err := Validate([]byte(`{"jsonrpc":"2.0","method":"personal_unlockAccount","params":[],"id":1}`)); !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("Validate() dangerous method error = %v, want ErrMethodNotAllowed", err)
	}
	if err := Validate([]byte(`{"jsonrpc":"2.0","method":"debug_traceTransaction","params":[],"id":1}`)); !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("Validate() debug method error = %v, want ErrMethodNotAllowed", err)
	}
}

func TestParseRequests(t *testing.T) {
	requests, err := ParseRequests([]byte(`[
		{"jsonrpc":"2.0","method":"eth_call","params":[],"id":"a"},
		{"jsonrpc":"2.0","method":"eth_blockNumber"}
	]`))
	if err != nil {
		t.Fatalf("ParseRequests() error = %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("len(requests) = %d, want 2", len(requests))
	}
	if requests[0].Method != "eth_call" || string(requests[0].ID) != `"a"` {
		t.Fatalf("unexpected first request: %+v", requests[0])
	}
	if !requests[1].Notification {
		t.Fatalf("second request should be a notification: %+v", requests[1])
	}
}

func TestAllowedIsExact(t *testing.T) {
	gate := MustNew(Config{AllowedMethods: []string{"eth_call"}})
	if !gate.Allowed("eth_call") {
		t.Fatal("Allowed(eth_call) = false")
	}
	for _, method := range []string{"eth_cal", "ETH_CALL", "eth_call ", "eth_call\n", "personal_unlockAccount"} {
		if gate.Allowed(method) {
			t.Fatalf("Allowed(%q) = true", method)
		}
	}
}

func TestErrorsIncludeUsefulContext(t *testing.T) {
	gate := testGate(t)
	err := gate.Validate([]byte(`[
		{"jsonrpc":"2.0","method":"eth_call","params":[],"id":1},
		{"jsonrpc":"2.0","method":"debug_traceCall","params":[],"id":2}
	]`))
	if err == nil || !strings.Contains(err.Error(), "debug_traceCall") {
		t.Fatalf("Validate() error = %v, want method context", err)
	}
}
