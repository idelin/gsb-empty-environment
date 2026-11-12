package netsec

import (
	"errors"
	"strings"
	"testing"
)

func testGate() *Gate {
	return NewGate(Config{
		AllowedMethods: []string{
			"eth_blockNumber",
			"eth_getLogs",
			"net_version",
			"web3_clientVersion",
		},
		MaxBatchSize:  3,
		MaxBlockRange: 100,
	})
}

func TestInspectDangerousSingleRequest(t *testing.T) {
	gate := testGate()
	cases := []string{
		`{"jsonrpc":"2.0","method":"personal_unlockAccount","params":["0xabc","pw"],"id":1}`,
		`{"jsonrpc":"2.0","method":"debug_traceTransaction","params":["0xtx"],"id":2}`,
		`{"jsonrpc":"2.0","method":"debug_traceCall","params":[],"id":3}`,
	}
	for _, body := range cases {
		err := gate.Inspect([]byte(body))
		if !errors.Is(err, ErrMethodNotAllowed) {
			t.Fatalf("body %s: got %v, want ErrMethodNotAllowed", body, err)
		}
	}
}

func TestInspectMethodExactCaseSensitiveMatch(t *testing.T) {
	gate := testGate()
	variants := []string{
		`{"jsonrpc":"2.0","method":"ETH_GETLOGS","params":[],"id":1}`,
		`{"jsonrpc":"2.0","method":"Eth_GetLogs","params":[],"id":2}`,
		`{"jsonrpc":"2.0","method":" eth_getLogs","params":[],"id":3}`,
		`{"jsonrpc":"2.0","method":"eth_getlogs","params":[],"id":4}`,
		`{"jsonrpc":"2.0","method":"personal_unlockaccount","params":[],"id":5}`,
	}
	for _, body := range variants {
		err := gate.Inspect([]byte(body))
		if !errors.Is(err, ErrMethodNotAllowed) {
			t.Fatalf("body %s: got %v, want ErrMethodNotAllowed", body, err)
		}
	}

	allowed := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	if err := gate.Inspect([]byte(allowed)); err != nil {
		t.Fatalf("whitelisted method rejected: %v", err)
	}
}

func TestInspectBatchRejectsWholeBatchOnDangerousElement(t *testing.T) {
	gate := testGate()
	body := []byte(`[
		{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1},
		{"jsonrpc":"2.0","method":"personal_unlockAccount","params":["0xabc","pw"],"id":2},
		{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x1","toBlock":"0x2"}],"id":3}
	]`)
	err := gate.Inspect(body)
	if !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("got %v, want ErrMethodNotAllowed for mixed batch", err)
	}

	debugBatch := []byte(`[
		{"jsonrpc":"2.0","method":"net_version","id":1},
		{"jsonrpc":"2.0","method":"debug_traceBlockByNumber","params":["0x10"],"id":2}
	]`)
	if err := gate.Inspect(debugBatch); !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("got %v, want ErrMethodNotAllowed for debug_* batch", err)
	}

	cleanBatch := []byte(`[
		{"jsonrpc":"2.0","method":"eth_blockNumber","id":1},
		{"jsonrpc":"2.0","method":"net_version","id":2}
	]`)
	if err := gate.Inspect(cleanBatch); err != nil {
		t.Fatalf("clean batch rejected: %v", err)
	}
}

func TestInspectRejectsDuplicateKeys(t *testing.T) {
	gate := testGate()
	cases := []struct {
		name string
		body string
	}{
		{
			name: "duplicate top-level method",
			body: `{"jsonrpc":"2.0","method":"eth_blockNumber","method":"personal_unlockAccount","id":1}`,
		},
		{
			name: "duplicate nested filter key",
			body: `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x1","fromBlock":"0x9","toBlock":"0x2"}],"id":1}`,
		},
		{
			name: "duplicate key inside batch element",
			body: `[{"jsonrpc":"2.0","method":"net_version","method":"debug_traceTransaction","id":1}]`,
		},
		{
			name: "duplicate jsonrpc key",
			body: `{"jsonrpc":"2.0","jsonrpc":"1.0","method":"net_version","id":1}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := gate.Inspect([]byte(tc.body))
			if !errors.Is(err, ErrDuplicateKey) {
				t.Fatalf("got %v, want ErrDuplicateKey", err)
			}
		})
	}
}

func TestInspectGetLogsBlockSpan(t *testing.T) {
	gate := testGate()
	cases := []struct {
		name      string
		body      string
		wantError error
	}{
		{
			name: "span within limit positional params",
			body: `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0x64"}],"id":1}`,
		},
		{
			name:      "span over limit",
			body:      `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0x65"}],"id":1}`,
			wantError: ErrBlockRangeTooLarge,
		},
		{
			name:      "huge hex span over limit",
			body:      `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x100","toBlock":"0xffffff"}],"id":1}`,
			wantError: ErrBlockRangeTooLarge,
		},
		{
			name: "named filter object params",
			body: `{"jsonrpc":"2.0","method":"eth_getLogs","params":{"fromBlock":"0x10","toBlock":"0x20","address":"0xabc"},"id":1}`,
		},
		{
			name: "block tag skips static check",
			body: `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"latest","toBlock":"latest"}],"id":1}`,
		},
		{
			name: "blockHash skips range check",
			body: `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"blockHash":"0xdead","fromBlock":"0x0","toBlock":"0xffff"}],"id":1}`,
		},
		{
			name:      "reversed numeric range",
			body:      `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x64","toBlock":"0x1"}],"id":1}`,
			wantError: ErrInvalidRequest,
		},
		{
			name: "no params allowed",
			body: `{"jsonrpc":"2.0","method":"eth_getLogs","id":1}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := gate.Inspect([]byte(tc.body))
			if tc.wantError == nil {
				if err != nil {
					t.Fatalf("unexpected rejection: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("got %v, want %v", err, tc.wantError)
			}
		})
	}
}

func TestInspectBatchSizeLimit(t *testing.T) {
	gate := testGate()
	buildBatch := func(n int) string {
		elements := make([]string, n)
		for i := range elements {
			elements[i] = `{"jsonrpc":"2.0","method":"net_version","id":1}`
		}
		return "[" + strings.Join(elements, ",") + "]"
	}

	if err := gate.Inspect([]byte(buildBatch(3))); err != nil {
		t.Fatalf("batch at limit rejected: %v", err)
	}
	err := gate.Inspect([]byte(buildBatch(4)))
	if !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("got %v, want ErrBatchTooLarge", err)
	}

	if err := gate.Inspect([]byte(`[]`)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty batch: got %v, want ErrInvalidRequest", err)
	}
}

func TestInspectMalformedRequests(t *testing.T) {
	gate := testGate()
	cases := []struct {
		name string
		body string
	}{
		{"not json", `not json at all`},
		{"scalar root", `"eth_blockNumber"`},
		{"wrong jsonrpc version", `{"jsonrpc":"1.0","method":"net_version","id":1}`},
		{"missing method", `{"jsonrpc":"2.0","id":1}`},
		{"unknown field", `{"jsonrpc":"2.0","method":"net_version","id":1,"extra":true}`},
		{"trailing data", `{"jsonrpc":"2.0","method":"net_version","id":1} garbage`},
		{"batch scalar element", `[1, 2]`},
		{"empty body", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := gate.Inspect([]byte(tc.body)); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("got %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestNewGateDefaultsAndWhitelist(t *testing.T) {
	gate := NewGate(Config{AllowedMethods: []string{"net_version"}})
	if gate.maxBatchSize != defaultMaxBatchSize {
		t.Fatalf("default batch size = %d, want %d", gate.maxBatchSize, defaultMaxBatchSize)
	}
	if gate.maxBlockRange != defaultMaxBlockRange {
		t.Fatalf("default block range = %d, want %d", gate.maxBlockRange, defaultMaxBlockRange)
	}
	if !gate.Allowed("net_version") || gate.Allowed("NET_VERSION") || gate.Allowed("debug_x") {
		t.Fatal("whitelist exact match behaves incorrectly")
	}
}

func TestInspectGetLogsEdgeCases(t *testing.T) {
	gate := testGate()
	cases := []struct {
		name      string
		body      string
		wantError error
	}{
		{"empty positional params", `{"jsonrpc":"2.0","method":"eth_getLogs","params":[],"id":1}`, nil},
		{"null filter element", `{"jsonrpc":"2.0","method":"eth_getLogs","params":[null],"id":1}`, nil},
		{"bare numeric blocks within limit", `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":0,"toBlock":100}],"id":1}`, nil},
		{"bare numeric blocks over limit", `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":0,"toBlock":101}],"id":1}`, ErrBlockRangeTooLarge},
		{"scalar params rejected", `{"jsonrpc":"2.0","method":"eth_getLogs","params":123,"id":1}`, ErrInvalidRequest},
		{"non-object filter element", `{"jsonrpc":"2.0","method":"eth_getLogs","params":[123],"id":1}`, ErrInvalidRequest},
		{"empty hex quantity", `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x","toBlock":"0x1"}],"id":1}`, ErrInvalidRequest},
		{"non-hex digits", `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0xzz","toBlock":"0x1"}],"id":1}`, ErrInvalidRequest},
		{"negative block number", `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":-1,"toBlock":1}],"id":1}`, ErrInvalidRequest},
		{"non-hex tag skips check", `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"earliest","toBlock":"0x64"}],"id":1}`, nil},
		{"multiple glued documents", `{"jsonrpc":"2.0","method":"net_version","id":1}{"jsonrpc":"2.0","method":"debug_x","id":2}`, ErrInvalidRequest},
		{"duplicate keys still caught inside loose filter", `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"toBlock":"0x1","toBlock":"0x9999","address":"0xabc"}],"id":1}`, ErrDuplicateKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := gate.Inspect([]byte(tc.body))
			if tc.wantError == nil {
				if err != nil {
					t.Fatalf("unexpected rejection: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("got %v, want %v", err, tc.wantError)
			}
		})
	}
}

func TestInspectBatchEntryLimitsAndGetLogsInBatch(t *testing.T) {
	gate := testGate()
	overSpanBatch := []byte(`[
		{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0x65"}],"id":1},
		{"jsonrpc":"2.0","method":"net_version","id":2}
	]`)
	if err := gate.Inspect(overSpanBatch); !errors.Is(err, ErrBlockRangeTooLarge) {
		t.Fatalf("batch with over-span getLogs: got %v, want ErrBlockRangeTooLarge", err)
	}

	nonObjectBatch := []byte(`[{"jsonrpc":"2.0","method":"net_version","id":1}, true]`)
	if err := gate.Inspect(nonObjectBatch); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("non-object batch element: got %v, want ErrInvalidRequest", err)
	}
}
