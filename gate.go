package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Sentinel errors returned by Gate.Inspect. Callers can use errors.Is to
// distinguish the rejection reason when producing proxy logs or RPC errors.
var (
	ErrInvalidRequest     = errors.New("invalid JSON-RPC 2.0 request")
	ErrDuplicateKey       = errors.New("duplicate JSON object key")
	ErrMethodNotAllowed   = errors.New("JSON-RPC method not allowed")
	ErrBatchTooLarge      = errors.New("JSON-RPC batch too large")
	ErrBlockRangeTooLarge = errors.New("eth_getLogs block range too large")
)

const (
	defaultMaxBatchSize  = 100
	defaultMaxBlockRange = 10_000
)

// Config configures a Gate. The method whitelist is matched exactly and
// case-sensitively: only listed strings are allowed.
type Config struct {
	AllowedMethods []string
	MaxBatchSize   int
	MaxBlockRange  uint64
}

// Gate is the JSON-RPC 2.0 method gate invoked at the reverse proxy request
// entry point. A zero-value Gate must not be used; construct one with NewGate.
type Gate struct {
	allowed       map[string]struct{}
	maxBatchSize  int
	maxBlockRange uint64
}

// NewGate builds a Gate. Zero-valued limits fall back to safe defaults.
func NewGate(cfg Config) *Gate {
	gate := &Gate{
		allowed:       make(map[string]struct{}, len(cfg.AllowedMethods)),
		maxBatchSize:  cfg.MaxBatchSize,
		maxBlockRange: cfg.MaxBlockRange,
	}
	if gate.maxBatchSize == 0 {
		gate.maxBatchSize = defaultMaxBatchSize
	}
	if gate.maxBlockRange == 0 {
		gate.maxBlockRange = defaultMaxBlockRange
	}
	for _, method := range cfg.AllowedMethods {
		gate.allowed[method] = struct{}{}
	}
	return gate
}

// Allowed reports whether method is present in the whitelist. Matching is
// exact and case-sensitive, so case variants never bypass the gate.
func (g *Gate) Allowed(method string) bool {
	_, ok := g.allowed[method]
	return ok
}

// Inspect validates a raw JSON-RPC 2.0 request body. A single request is
// checked directly; for a batch, every element must pass or the whole batch is
// rejected. Duplicate object keys at any depth are rejected, method names must
// exactly match the whitelist, and eth_getLogs calls are subject to the block
// span limit.
func (g *Gate) Inspect(body []byte) error {
	if err := rejectDuplicateKeys(body); err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return fmt.Errorf("%w: empty body", ErrInvalidRequest)
	}
	switch trimmed[0] {
	case '{':
		request, err := decodeRequest(trimmed)
		if err != nil {
			return err
		}
		return g.inspectRequest(request)
	case '[':
		return g.inspectBatch(trimmed)
	default:
		return fmt.Errorf("%w: request must be a JSON object or array", ErrInvalidRequest)
	}
}

func (g *Gate) inspectBatch(raw []byte) error {
	var batch []json.RawMessage
	if err := strictDecode(raw, &batch); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if len(batch) == 0 {
		return fmt.Errorf("%w: empty batch array", ErrInvalidRequest)
	}
	if len(batch) > g.maxBatchSize {
		return fmt.Errorf("%w: %d requests exceed limit %d", ErrBatchTooLarge, len(batch), g.maxBatchSize)
	}
	for _, item := range batch {
		element := bytes.TrimSpace(item)
		if len(element) == 0 || element[0] != '{' {
			return fmt.Errorf("%w: batch element must be a request object", ErrInvalidRequest)
		}
		request, err := decodeRequest(element)
		if err != nil {
			return err
		}
		if err := g.inspectRequest(request); err != nil {
			return err
		}
	}
	return nil
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      json.RawMessage `json:"id"`
}

func decodeRequest(raw []byte) (*rpcRequest, error) {
	var request rpcRequest
	if err := strictDecode(raw, &request); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if request.JSONRPC != "2.0" {
		return nil, fmt.Errorf("%w: jsonrpc member must be \"2.0\"", ErrInvalidRequest)
	}
	if request.Method == "" {
		return nil, fmt.Errorf("%w: method must be a non-empty string", ErrInvalidRequest)
	}
	return &request, nil
}

func (g *Gate) inspectRequest(request *rpcRequest) error {
	if !g.Allowed(request.Method) {
		return fmt.Errorf("%w: %q", ErrMethodNotAllowed, request.Method)
	}
	if request.Method == "eth_getLogs" {
		return g.inspectGetLogs(request.Params)
	}
	return nil
}

type logFilter struct {
	BlockHash json.RawMessage `json:"blockHash"`
	FromBlock json.RawMessage `json:"fromBlock"`
	ToBlock   json.RawMessage `json:"toBlock"`
}

func (g *Gate) inspectGetLogs(params json.RawMessage) error {
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var filterRaw json.RawMessage
	switch trimmed[0] {
	case '[':
		var positional []json.RawMessage
		if err := strictDecode(trimmed, &positional); err != nil {
			return fmt.Errorf("%w: malformed eth_getLogs params: %v", ErrInvalidRequest, err)
		}
		if len(positional) == 0 {
			return nil
		}
		filterRaw = positional[0]
	case '{':
		filterRaw = trimmed
	default:
		return fmt.Errorf("%w: eth_getLogs params must be an array or object", ErrInvalidRequest)
	}

	filterRaw = bytes.TrimSpace(filterRaw)
	if len(filterRaw) == 0 || bytes.Equal(filterRaw, []byte("null")) {
		return nil
	}
	var filter logFilter
	// Duplicate keys are already rejected by the tokenizer pass; decode the
	// filter permissively because standard fields (address, topics, ...) are
	// irrelevant to the span check.
	if err := json.Unmarshal(filterRaw, &filter); err != nil {
		return fmt.Errorf("%w: malformed eth_getLogs filter: %v", ErrInvalidRequest, err)
	}
	if len(bytes.TrimSpace(filter.BlockHash)) > 0 {
		return nil
	}

	from, fromNumeric, err := parseBlockNumber(filter.FromBlock)
	if err != nil {
		return fmt.Errorf("%w: invalid fromBlock: %v", ErrInvalidRequest, err)
	}
	to, toNumeric, err := parseBlockNumber(filter.ToBlock)
	if err != nil {
		return fmt.Errorf("%w: invalid toBlock: %v", ErrInvalidRequest, err)
	}
	if !fromNumeric || !toNumeric {
		return nil
	}
	if to < from {
		return fmt.Errorf("%w: toBlock %d is below fromBlock %d", ErrInvalidRequest, to, from)
	}
	if span := to - from; span > g.maxBlockRange {
		return fmt.Errorf("%w: span %d exceeds limit %d", ErrBlockRangeTooLarge, span, g.maxBlockRange)
	}
	return nil
}

// parseBlockNumber accepts hex quantities ("0x1a4") and bare JSON numbers.
// Block tags such as "latest" and omitted/null bounds are not numeric, so the
// caller skips the static span check for them.
func parseBlockNumber(raw json.RawMessage) (value uint64, numeric bool, err error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return 0, false, nil
	}
	if text[0] == '"' {
		var quoted string
		if err := json.Unmarshal(raw, &quoted); err != nil {
			return 0, false, err
		}
		if !strings.HasPrefix(quoted, "0x") && !strings.HasPrefix(quoted, "0X") {
			return 0, false, nil
		}
		if len(quoted) == 2 {
			return 0, false, errors.New("empty hex quantity")
		}
		value, err = strconv.ParseUint(quoted[2:], 16, 64)
		return value, err == nil, err
	}
	value, err = strconv.ParseUint(text, 10, 64)
	return value, err == nil, err
}

// strictDecode rejects unknown fields and trailing data after the JSON value.
func strictDecode(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing data")
		}
		return err
	}
	return nil
}

// rejectDuplicateKeys tokenizes the whole document and rejects repeated keys
// at any object depth. encoding/json silently keeps the last duplicate, which
// would let a crafted request hide the real method from a naive struct decode.
func rejectDuplicateKeys(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := walkDuplicateKeys(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: trailing data after JSON document", ErrInvalidRequest)
		}
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return nil
}

func walkDuplicateKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: empty body", ErrInvalidRequest)
		}
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
			}
			key := keyToken.(string)
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("%w: %q", ErrDuplicateKey, key)
			}
			seen[key] = struct{}{}
			if err := walkDuplicateKeys(decoder); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
	case '[':
		for decoder.More() {
			if err := walkDuplicateKeys(decoder); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
	}
	return nil
}
