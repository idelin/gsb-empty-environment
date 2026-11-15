// Package methodgate provides a request-entry guard for a reverse proxy that
// sits in front of a blockchain JSON-RPC 2.0 endpoint.
//
// It enforces:
//   - strict JSON-RPC 2.0 framing (single request or batch array);
//   - rejection of JSON objects containing duplicate keys;
//   - exact, case-sensitive method-name matching against an allow list;
//   - all-or-nothing batch handling: one disallowed element rejects the batch;
//   - a cap on the number of requests in a batch;
//   - a cap on the block span of eth_getLogs filters.
package methodgate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
)

// Standard JSON-RPC 2.0 error codes plus an extension range for policy errors.
const (
	CodeParseError      = -32700
	CodeInvalidRequest  = -32600
	CodeMethodNotFound  = -32601
	CodeInvalidParams   = -32602
	CodePolicyViolation = -32000
)

// Default limits applied by New when the corresponding Config field is zero.
const (
	// DefaultMaxBatchSize caps the number of elements in a batch request.
	DefaultMaxBatchSize = 100
	// DefaultMaxGetLogsSpan caps toBlock-fromBlock for eth_getLogs.
	DefaultMaxGetLogsSpan = 10_000
)

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message)
}

// Config configures a Gate. The zero value is not usable directly; call New.
type Config struct {
	// AllowedMethods is the exact, case-sensitive allow list. Every request
	// method must appear verbatim in this map.
	AllowedMethods map[string]bool
	// MaxBatchSize limits the number of elements allowed in one batch. Zero
	// uses DefaultMaxBatchSize.
	MaxBatchSize int
	// MaxGetLogsSpan limits toBlock-fromBlock when both are block numbers.
	// Zero uses DefaultMaxGetLogsSpan.
	MaxGetLogsSpan uint64
}

// Gate is the stateless method gate used by the proxy request entry.
type Gate struct {
	allowed        map[string]bool
	maxBatch       int
	maxGetLogsSpan uint64
}

// New builds a Gate from cfg. An empty allow list is rejected because a gate
// that permits nothing useful (or, worse, is misconfigured to permit
// everything) must be created explicitly.
func New(cfg Config) (*Gate, error) {
	if len(cfg.AllowedMethods) == 0 {
		return nil, errors.New("methodgate: AllowedMethods must not be empty")
	}
	allowed := make(map[string]bool, len(cfg.AllowedMethods))
	for method := range cfg.AllowedMethods {
		allowed[method] = true
	}
	maxBatch := cfg.MaxBatchSize
	if maxBatch <= 0 {
		maxBatch = DefaultMaxBatchSize
	}
	maxSpan := cfg.MaxGetLogsSpan
	if maxSpan == 0 {
		maxSpan = DefaultMaxGetLogsSpan
	}
	return &Gate{
		allowed:        allowed,
		maxBatch:       maxBatch,
		maxGetLogsSpan: maxSpan,
	}, nil
}

// DefaultAllowedMethods returns a conservative allow list covering common
// read-only Ethereum JSON-RPC methods. Sensitive namespaces such as
// personal_*, debug_*, admin_*, txpool_* and miner_* are intentionally absent.
func DefaultAllowedMethods() map[string]bool {
	return map[string]bool{
		"eth_blockNumber":          true,
		"eth_call":                 true,
		"eth_chainId":              true,
		"eth_estimateGas":          true,
		"eth_gasPrice":             true,
		"eth_getBalance":           true,
		"eth_getBlockByHash":       true,
		"eth_getBlockByNumber":     true,
		"eth_getCode":              true,
		"eth_getLogs":              true,
		"eth_getStorageAt":         true,
		"eth_getTransactionByHash": true,
		"eth_getTransactionByBlockNumberAndIndex": true,
		"eth_getTransactionReceipt":               true,
		"eth_getTransactionCount":                 true,
		"eth_sendRawTransaction":                  true,
		"net_listening":                           true,
		"net_peerCount":                           true,
		"net_version":                             true,
		"rpc_modules":                             true,
		"web3_clientVersion":                      true,
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      json.RawMessage `json:"id"`
}

// Inspect validates body against the gate policy. A nil error means the
// request is safe to forward. A non-nil error is always an *RPCError so the
// caller can serialize a JSON-RPC 2.0 error response.
func (g *Gate) Inspect(body []byte) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return &RPCError{Code: CodeParseError, Message: "parse error: empty request body"}
	}

	if err := rejectDuplicateKeys(trimmed); err != nil {
		return &RPCError{Code: CodeParseError, Message: err.Error()}
	}

	// A batch is a JSON array; a single request is a JSON object.
	if trimmed[0] == '[' {
		return g.inspectBatch(trimmed)
	}
	return g.inspectSingle(trimmed)
}

func (g *Gate) inspectBatch(raw json.RawMessage) error {
	var batch []json.RawMessage
	if err := json.Unmarshal(raw, &batch); err != nil {
		return &RPCError{Code: CodeParseError, Message: "parse error: invalid JSON-RPC batch"}
	}
	if len(batch) == 0 {
		return &RPCError{Code: CodeInvalidRequest, Message: "invalid request: empty batch"}
	}
	if len(batch) > g.maxBatch {
		return &RPCError{
			Code:    CodePolicyViolation,
			Message: fmt.Sprintf("batch size %d exceeds limit of %d", len(batch), g.maxBatch),
		}
	}
	// All-or-nothing: the first disallowed element rejects the whole batch.
	for index, element := range batch {
		if err := g.inspectSingle(bytes.TrimSpace(element)); err != nil {
			var rpcErr *RPCError
			if errors.As(err, &rpcErr) {
				rpcErr.Message = fmt.Sprintf("batch element %d rejected: %s", index, rpcErr.Message)
				return rpcErr
			}
			return err
		}
	}
	return nil
}

func (g *Gate) inspectSingle(raw json.RawMessage) error {
	if len(raw) == 0 || raw[0] != '{' {
		return &RPCError{Code: CodeInvalidRequest, Message: "invalid request: expected a JSON-RPC request object"}
	}

	var req rpcRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&req); err != nil {
		return &RPCError{Code: CodeParseError, Message: "parse error: malformed JSON-RPC request"}
	}

	if req.JSONRPC != "2.0" {
		return &RPCError{Code: CodeInvalidRequest, Message: `invalid request: jsonrpc must be "2.0"`}
	}
	if strings.TrimSpace(req.Method) == "" {
		return &RPCError{Code: CodeInvalidRequest, Message: "invalid request: missing method"}
	}

	// Exact, case-sensitive matching. Lowercasing or prefix matching would let
	// DEBUG_* or debug_Foo variants slip past the gate.
	if !g.allowed[req.Method] {
		return &RPCError{
			Code:    CodeMethodNotFound,
			Message: fmt.Sprintf("method %q is not allowed", req.Method),
		}
	}

	if req.Method == "eth_getLogs" {
		if err := g.inspectGetLogs(req.Params); err != nil {
			return err
		}
	}
	return nil
}

type logFilter struct {
	FromBlock json.RawMessage `json:"fromBlock"`
	ToBlock   json.RawMessage `json:"toBlock"`
}

func (g *Gate) inspectGetLogs(params json.RawMessage) error {
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}

	// The common shape is a single-element positional array [filter]; also
	// accept a bare filter object for robustness.
	var filter logFilter
	if trimmed[0] == '[' {
		var positional []json.RawMessage
		if err := json.Unmarshal(trimmed, &positional); err != nil {
			return &RPCError{Code: CodeInvalidParams, Message: "invalid params: cannot decode eth_getLogs params"}
		}
		if len(positional) == 0 {
			return nil
		}
		if err := json.Unmarshal(positional[0], &filter); err != nil {
			return &RPCError{Code: CodeInvalidParams, Message: "invalid params: malformed eth_getLogs filter"}
		}
	} else if trimmed[0] == '{' {
		if err := json.Unmarshal(trimmed, &filter); err != nil {
			return &RPCError{Code: CodeInvalidParams, Message: "invalid params: malformed eth_getLogs filter"}
		}
	} else {
		return &RPCError{Code: CodeInvalidParams, Message: "invalid params: eth_getLogs expects a filter object"}
	}

	from, fromOK, err := decodeBlockNumber(filter.FromBlock)
	if err != nil {
		return &RPCError{Code: CodeInvalidParams, Message: "invalid params: fromBlock is not a block number"}
	}
	to, toOK, err := decodeBlockNumber(filter.ToBlock)
	if err != nil {
		return &RPCError{Code: CodeInvalidParams, Message: "invalid params: toBlock is not a block number"}
	}

	// Tagged blocks (e.g. "latest", "pending") carry no computable span and are
	// left to the upstream node; only numeric ranges are gated here.
	if !fromOK || !toOK {
		return nil
	}
	if to < from {
		return &RPCError{Code: CodeInvalidParams, Message: "invalid params: toBlock precedes fromBlock"}
	}
	span := to - from
	if span > g.maxGetLogsSpan {
		return &RPCError{
			Code:    CodePolicyViolation,
			Message: fmt.Sprintf("eth_getLogs block span %d exceeds limit of %d", span, g.maxGetLogsSpan),
		}
	}
	return nil
}

// decodeBlockNumber decodes a quantity ("0x..." hex or a JSON number) into a
// block number. The boolean reports whether the value was numeric at all; a
// named block tag such as "latest" yields ok=false with no error.
func decodeBlockNumber(raw json.RawMessage) (uint64, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return 0, false, nil
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return 0, false, err
		}
		if !strings.HasPrefix(value, "0x") && !strings.HasPrefix(value, "0X") {
			return 0, false, nil
		}
		number, ok := new(big.Int).SetString(value[2:], 16)
		if !ok || number.Sign() < 0 {
			return 0, false, fmt.Errorf("invalid hex block number %q", value)
		}
		if !number.IsUint64() {
			return 0, false, fmt.Errorf("block number %q overflows uint64", value)
		}
		return number.Uint64(), true, nil
	}
	var number uint64
	if err := json.Unmarshal(trimmed, &number); err != nil {
		return 0, false, err
	}
	return number, true, nil
}

// frame tracks one JSON object or array while scanning for duplicate keys.
type frame struct {
	isArray   bool
	entries   map[string]struct{}
	expectKey bool
}

// rejectDuplicateKeys tokenizes raw and rejects any JSON object that defines
// the same key more than once. encoding/json silently keeps the last value,
// which an attacker can abuse to smuggle a dangerous method past a naive check.
func rejectDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var stack []*frame
	for {
		token, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("parse error: %w", err)
		}

		delim, ok := token.(json.Delim)
		if ok {
			switch delim {
			case '{':
				stack = append(stack, &frame{entries: make(map[string]struct{}), expectKey: true})
				continue
			case '[':
				stack = append(stack, &frame{isArray: true})
				continue
			case '}', ']':
				if len(stack) == 0 {
					return errors.New("parse error: unbalanced JSON delimiter")
				}
				stack = stack[:len(stack)-1]
				// Fall through: the completed container is itself the value of
				// the enclosing object, which next expects another key.
			}
		} else if len(stack) > 0 {
			top := stack[len(stack)-1]
			if !top.isArray && top.expectKey {
				key, isString := token.(string)
				if !isString {
					return errors.New("parse error: object key is not a string")
				}
				if _, exists := top.entries[key]; exists {
					return fmt.Errorf("parse error: duplicate key %q", key)
				}
				top.entries[key] = struct{}{}
				top.expectKey = false
				continue
			}
		}

		// A scalar value, or a just-closed nested container, completes a
		// key/value pair on the enclosing object, which then wants a key.
		if len(stack) > 0 {
			parent := stack[len(stack)-1]
			if !parent.isArray {
				parent.expectKey = true
			}
		}
	}

	if len(stack) != 0 {
		return errors.New("parse error: unbalanced JSON delimiter")
	}
	return nil
}
