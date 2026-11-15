package methodgate

import (
	"errors"
	"strings"
	"testing"
)

func newTestGate(t *testing.T) *Gate {
	t.Helper()
	gate, err := New(Config{
		AllowedMethods: map[string]bool{
			"eth_chainId":     true,
			"eth_getLogs":     true,
			"eth_blockNumber": true,
		},
		MaxBatchSize:   3,
		MaxGetLogsSpan: 100,
	})
	if err != nil {
		t.Fatalf("New returned unexpected error: %v", err)
	}
	return gate
}

func requireCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %d, got nil", want)
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected *RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != want {
		t.Fatalf("expected code %d, got %d (%s)", want, rpcErr.Code, rpcErr.Message)
	}
}

func TestInspect_AllowsWellFormedSingleRequest(t *testing.T) {
	gate := newTestGate(t)
	body := []byte(`{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}`)
	if err := gate.Inspect(body); err != nil {
		t.Fatalf("expected request to pass, got %v", err)
	}
}

func TestInspect_BlocksDangerousMethodsExactly(t *testing.T) {
	gate := newTestGate(t)
	cases := map[string]string{
		"personal namespace": `{"jsonrpc":"2.0","method":"personal_unlockAccount","params":[],"id":1}`,
		"debug namespace":    `{"jsonrpc":"2.0","method":"debug_traceTransaction","params":[],"id":1}`,
		"upper case variant": `{"jsonrpc":"2.0","method":"ETH_CHAINID","params":[],"id":1}`,
		"mixed case variant": `{"jsonrpc":"2.0","method":"Eth_ChainId","params":[],"id":1}`,
		"prefix spoof":       `{"jsonrpc":"2.0","method":"eth_chainIdX","params":[],"id":1}`,
		"debug prefix only":  `{"jsonrpc":"2.0","method":"debug_","params":[],"id":1}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			err := gate.Inspect([]byte(body))
			requireCode(t, err, CodeMethodNotFound)
		})
	}
}

func TestInspect_BatchRejectsWholeBatchWhenOneElementDangerous(t *testing.T) {
	gate := newTestGate(t)
	// The first element is allowed, the second reaches personal_unlockAccount.
	body := []byte(`[
		{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1},
		{"jsonrpc":"2.0","method":"personal_unlockAccount","params":["0xaddr","secret"],"id":2}
	]`)
	err := gate.Inspect(body)
	requireCode(t, err, CodeMethodNotFound)
	if !strings.Contains(err.Error(), "batch element 1 rejected") {
		t.Fatalf("expected error to identify the offending element, got %v", err)
	}

	// A debug_* element hidden inside an otherwise clean batch also rejects.
	debugBody := []byte(`[
		{"jsonrpc":"2.0","method":"eth_chainId","id":1},
		{"jsonrpc":"2.0","method":"debug_traceBlockByNumber","params":["0x1"],"id":2}
	]`)
	requireCode(t, gate.Inspect(debugBody), CodeMethodNotFound)
}

func TestInspect_AllowsCleanBatch(t *testing.T) {
	gate := newTestGate(t)
	body := []byte(`[
		{"jsonrpc":"2.0","method":"eth_chainId","id":1},
		{"jsonrpc":"2.0","method":"eth_blockNumber","id":2}
	]`)
	if err := gate.Inspect(body); err != nil {
		t.Fatalf("expected clean batch to pass, got %v", err)
	}
}

func TestInspect_RejectsOversizedBatch(t *testing.T) {
	gate := newTestGate(t) // MaxBatchSize = 3
	body := []byte(`[
		{"jsonrpc":"2.0","method":"eth_chainId","id":1},
		{"jsonrpc":"2.0","method":"eth_chainId","id":2},
		{"jsonrpc":"2.0","method":"eth_chainId","id":3},
		{"jsonrpc":"2.0","method":"eth_chainId","id":4}
	]`)
	requireCode(t, gate.Inspect(body), CodePolicyViolation)
}

func TestInspect_RejectsEmptyBatch(t *testing.T) {
	gate := newTestGate(t)
	requireCode(t, gate.Inspect([]byte(`[]`)), CodeInvalidRequest)
}

func TestInspect_BatchAtLimitAndNestedArraysPass(t *testing.T) {
	gate := newTestGate(t) // MaxBatchSize = 3
	body := []byte(`[
		{"jsonrpc":"2.0","method":"eth_chainId","id":1},
		{"jsonrpc":"2.0","method":"eth_chainId","id":2},
		{"jsonrpc":"2.0","method":"eth_chainId","id":3}
	]`)
	if err := gate.Inspect(body); err != nil {
		t.Fatalf("batch at exactly the limit should pass, got %v", err)
	}

	// Repeated objects inside an array share key names but are NOT duplicate
	// keys; the scanner must treat each object independently.
	nestedArray := []byte(`{
		"jsonrpc":"2.0","method":"eth_chainId","id":1,
		"params":[[{"address":"0xa"},{"address":"0xb"}],[{"address":"0xc"}]]
	}`)
	if err := gate.Inspect(nestedArray); err != nil {
		t.Fatalf("distinct nested objects must not trigger duplicate-key error, got %v", err)
	}
}

func TestInspect_RejectsDuplicateMethodKey(t *testing.T) {
	gate := newTestGate(t)
	// encoding/json would keep the last (benign-looking) value; the gate must
	// refuse to make any decision on an ambiguous object instead.
	body := []byte(`{"jsonrpc":"2.0","method":"personal_unlockAccount","method":"eth_chainId","id":1}`)
	requireCode(t, gate.Inspect(body), CodeParseError)

	// Reverse ordering must be rejected just the same.
	body = []byte(`{"jsonrpc":"2.0","method":"eth_chainId","method":"debug_traceTransaction","id":1}`)
	requireCode(t, gate.Inspect(body), CodeParseError)
}

func TestInspect_RejectsDuplicateKeysInNestedAndBatchShapes(t *testing.T) {
	gate := newTestGate(t)
	nested := []byte(`{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x1","fromBlock":"0x2"}],"id":1}`)
	requireCode(t, gate.Inspect(nested), CodeParseError)

	batched := []byte(`[
		{"jsonrpc":"2.0","id":1,"method":"eth_chainId","id":2}
	]`)
	requireCode(t, gate.Inspect(batched), CodeParseError)

	// Repeated keys inside array objects must not be mistaken for array dupes.
	arrayObject := []byte(`{"jsonrpc":"2.0","method":"eth_getLogs","params":[[{"a":1,"a":2}]],"id":1}`)
	requireCode(t, gate.Inspect(arrayObject), CodeParseError)
}

func TestInspect_GetLogsSpanLimit(t *testing.T) {
	gate := newTestGate(t) // span limit = 100

	pass := []byte(`{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0x64"}],"id":1}`)
	if err := gate.Inspect(pass); err != nil {
		t.Fatalf("span of exactly 100 should pass, got %v", err)
	}

	over := []byte(`{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0x65"}],"id":1}`)
	requireCode(t, gate.Inspect(over), CodePolicyViolation)

	// Numeric JSON block numbers (not just 0x hex) are measured too.
	overDecimal := []byte(`{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":10,"toBlock":200}],"id":1}`)
	requireCode(t, gate.Inspect(overDecimal), CodePolicyViolation)

	// Named tags have no measurable span and are left to the upstream node.
	tagged := []byte(`{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"latest"}],"id":1}`)
	if err := gate.Inspect(tagged); err != nil {
		t.Fatalf("tagged toBlock should pass, got %v", err)
	}

	// Reversed numeric range is invalid params.
	reversed := []byte(`{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x10","toBlock":"0x1"}],"id":1}`)
	requireCode(t, gate.Inspect(reversed), CodeInvalidParams)
}

func TestInspect_RejectsMalformedAndNonJSONRPC(t *testing.T) {
	gate := newTestGate(t)
	cases := []struct {
		name string
		body string
		code int
	}{
		{"empty", "", CodeParseError},
		{"not json", `not-json`, CodeParseError},
		{"wrong version", `{"jsonrpc":"1.0","method":"eth_chainId","id":1}`, CodeInvalidRequest},
		{"missing method", `{"jsonrpc":"2.0","id":1}`, CodeInvalidRequest},
		{"batch element not object", `[42]`, CodeInvalidRequest},
		{"duplicate jsonrpc key", `{"jsonrpc":"2.0","jsonrpc":"2.0","method":"eth_chainId"}`, CodeParseError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireCode(t, gate.Inspect([]byte(tc.body)), tc.code)
		})
	}
}

func TestNew_RejectsEmptyAllowList(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error for empty allow list")
	}
}

func TestDefaultAllowedMethods_ExcludesSensitiveNamespaces(t *testing.T) {
	allowed := DefaultAllowedMethods()
	for _, dangerous := range []string{
		"personal_unlockAccount",
		"personal_importRawKey",
		"debug_traceTransaction",
		"debug_traceCall",
		"admin_nodeInfo",
		"miner_start",
		"txpool_content",
	} {
		if allowed[dangerous] {
			t.Fatalf("default allow list must not include %q", dangerous)
		}
	}
	if !allowed["eth_getLogs"] {
		t.Fatal("expected eth_getLogs in default allow list")
	}
}
