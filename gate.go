package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	DefaultMaxBatchSize              = 100
	DefaultMaxGetLogsBlockSpan       = 10_000
	DefaultMaxBodyBytes        int64 = 1 << 20

	maxJSONDepth = 64
)

var (
	ErrInvalidRequest      = errors.New("invalid JSON-RPC request")
	ErrDuplicateKey        = errors.New("duplicate JSON object key")
	ErrCaseVariantKey      = errors.New("case-variant JSON-RPC key")
	ErrBatchTooLarge       = errors.New("JSON-RPC batch too large")
	ErrMethodNotAllowed    = errors.New("JSON-RPC method not allowed")
	ErrBlockRangeTooLarge  = errors.New("eth_getLogs block range exceeds limit")
	ErrUnboundedBlockRange = errors.New("eth_getLogs block range cannot be bounded")
	ErrRequestTooLarge     = errors.New("request body too large")
)

var DefaultAllowedMethods = []string{
	"eth_blobBaseFee",
	"eth_blockNumber",
	"eth_call",
	"eth_chainId",
	"eth_createAccessList",
	"eth_estimateGas",
	"eth_feeHistory",
	"eth_gasPrice",
	"eth_getBalance",
	"eth_getBlockByHash",
	"eth_getBlockByNumber",
	"eth_getBlockReceipts",
	"eth_getBlockTransactionCountByHash",
	"eth_getBlockTransactionCountByNumber",
	"eth_getCode",
	"eth_getLogs",
	"eth_getProof",
	"eth_getStorageAt",
	"eth_getTransactionByBlockHashAndIndex",
	"eth_getTransactionByBlockNumberAndIndex",
	"eth_getTransactionByHash",
	"eth_getTransactionCount",
	"eth_getTransactionReceipt",
	"eth_getUncleByBlockHashAndIndex",
	"eth_getUncleByBlockNumberAndIndex",
	"eth_getUncleCountByBlockHash",
	"eth_getUncleCountByBlockNumber",
	"eth_maxPriorityFeePerGas",
	"eth_protocolVersion",
	"eth_syncing",
	"net_listening",
	"net_peerCount",
	"net_version",
	"web3_clientVersion",
	"web3_sha3",
}

type Config struct {
	AllowedMethods []string

	MaxBatchSize        int
	MaxGetLogsBlockSpan uint64
	MaxBlockSpan        uint64 // Deprecated alias for MaxGetLogsBlockSpan.
	MaxBodyBytes        int64
}

type Policy = Config

type Gate struct {
	allowed             map[string]struct{}
	maxBatchSize        int
	maxGetLogsBlockSpan uint64
	maxBodyBytes        int64
}

type Request struct {
	JSONRPC      string
	Method       string
	Params       json.RawMessage
	ID           json.RawMessage
	Notification bool
}

func New(cfg Config) (*Gate, error) {
	methods := cfg.AllowedMethods
	if methods == nil {
		methods = DefaultAllowedMethods
	}

	allowed := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		if method == "" || strings.TrimSpace(method) != method {
			return nil, fmt.Errorf("invalid allowed method %q", method)
		}
		allowed[method] = struct{}{}
	}

	maxBatchSize := cfg.MaxBatchSize
	if maxBatchSize <= 0 {
		maxBatchSize = DefaultMaxBatchSize
	}

	maxBlockSpan := cfg.MaxGetLogsBlockSpan
	if maxBlockSpan == 0 {
		maxBlockSpan = cfg.MaxBlockSpan
	}
	if maxBlockSpan == 0 {
		maxBlockSpan = DefaultMaxGetLogsBlockSpan
	}

	maxBodyBytes := cfg.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxBodyBytes
	}

	return &Gate{
		allowed:             allowed,
		maxBatchSize:        maxBatchSize,
		maxGetLogsBlockSpan: maxBlockSpan,
		maxBodyBytes:        maxBodyBytes,
	}, nil
}

func NewGate(cfg Config) (*Gate, error) {
	return New(cfg)
}

func NewPolicy(cfg Config) (*Gate, error) {
	return New(cfg)
}

func MustNew(cfg Config) *Gate {
	gate, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return gate
}

var defaultGate = MustNew(Config{})

func Validate(body []byte) error {
	return defaultGate.Validate(body)
}

func Check(body []byte) error {
	return defaultGate.Validate(body)
}

func Middleware(next http.Handler) http.Handler {
	return defaultGate.Middleware(next)
}

func Handler(next http.Handler) http.Handler {
	return defaultGate.Middleware(next)
}

func (g *Gate) Validate(body []byte) error {
	if g == nil {
		return fmt.Errorf("%w: nil gate", ErrInvalidRequest)
	}
	if int64(len(body)) > g.maxBodyBytes {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, ErrRequestTooLarge)
	}

	requests, err := decodeBody(body, g.maxBatchSize)
	if err != nil {
		return err
	}

	for _, request := range requests {
		if _, ok := g.allowed[request.Method]; !ok {
			return fmt.Errorf("%w: %q", ErrMethodNotAllowed, request.Method)
		}
	}
	for _, request := range requests {
		if request.Method == "eth_getLogs" {
			if err := g.validateGetLogs(request.Params); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *Gate) Check(body []byte) error {
	return g.Validate(body)
}

func (g *Gate) Allowed(method string) bool {
	if g == nil {
		return false
	}
	_, ok := g.allowed[method]
	return ok
}

func Parse(body []byte) ([]Request, error) {
	return decodeBody(body, 0)
}

func ParseRequests(body []byte) ([]Request, error) {
	return decodeBody(body, 0)
}

func decodeBody(body []byte, maxBatchSize int) ([]Request, error) {
	if err := checkUniqueKeys(body); err != nil {
		return nil, err
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, invalid("empty request body")
	}

	switch trimmed[0] {
	case '{':
		request, err := decodeRequest(trimmed)
		if err != nil {
			return nil, err
		}
		return []Request{request}, nil
	case '[':
		var batch []json.RawMessage
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			return nil, invalid("malformed batch: %v", err)
		}
		if len(batch) == 0 {
			return nil, invalid("empty batch")
		}
		if maxBatchSize > 0 && len(batch) > maxBatchSize {
			return nil, fmt.Errorf("%w: %w: got %d, limit %d", ErrInvalidRequest, ErrBatchTooLarge, len(batch), maxBatchSize)
		}

		requests := make([]Request, 0, len(batch))
		for i, raw := range batch {
			request, err := decodeRequest(raw)
			if err != nil {
				return nil, fmt.Errorf("batch element %d: %w", i, err)
			}
			requests = append(requests, request)
		}
		return requests, nil
	default:
		return nil, invalid("request must be a JSON object or batch array")
	}
}

func decodeRequest(raw []byte) (Request, error) {
	if firstNonSpace(raw) != '{' {
		return Request{}, invalid("request element must be an object")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Request{}, invalid("malformed request object: %v", err)
	}
	if fields == nil {
		return Request{}, invalid("request element must be an object")
	}

	if err := checkCanonicalKeys(fields); err != nil {
		return Request{}, err
	}

	versionRaw, ok := fields["jsonrpc"]
	if !ok {
		return Request{}, invalid("missing jsonrpc version")
	}
	var version string
	if err := json.Unmarshal(versionRaw, &version); err != nil || version != "2.0" {
		return Request{}, invalid("jsonrpc version must be exactly 2.0")
	}

	methodRaw, ok := fields["method"]
	if !ok {
		return Request{}, invalid("missing method")
	}
	var method string
	if err := json.Unmarshal(methodRaw, &method); err != nil || method == "" {
		return Request{}, invalid("method must be a non-empty string")
	}

	paramsRaw, hasParams := fields["params"]
	if hasParams && !isStructured(paramsRaw) {
		return Request{}, invalid("params must be an array or object")
	}

	idRaw, hasID := fields["id"]
	if hasID {
		if err := validateID(idRaw); err != nil {
			return Request{}, err
		}
	}

	return Request{
		JSONRPC:      version,
		Method:       method,
		Params:       cloneRaw(paramsRaw),
		ID:           cloneRaw(idRaw),
		Notification: !hasID,
	}, nil
}

func checkCanonicalKeys(fields map[string]json.RawMessage) error {
	canonical := map[string]string{
		"id":      "id",
		"jsonrpc": "jsonrpc",
		"method":  "method",
		"params":  "params",
	}
	for key := range fields {
		folded := strings.ToLower(key)
		if want, ok := canonical[folded]; ok && key != want {
			return fmt.Errorf("%w: %w: %q", ErrInvalidRequest, ErrCaseVariantKey, key)
		}
	}
	return nil
}

func validateID(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return invalid("invalid id: %v", err)
	}
	switch value.(type) {
	case nil, string, json.Number:
		return nil
	default:
		return invalid("id must be a string, number, or null")
	}
}

func (g *Gate) validateGetLogs(params json.RawMessage) error {
	if len(bytes.TrimSpace(params)) == 0 {
		return nil
	}
	if firstNonSpace(params) != '[' {
		return invalid("eth_getLogs params must be an array")
	}

	var args []json.RawMessage
	if err := json.Unmarshal(params, &args); err != nil {
		return invalid("invalid eth_getLogs params: %v", err)
	}
	if len(args) == 0 {
		return nil
	}
	if firstNonSpace(args[0]) != '{' {
		return invalid("eth_getLogs filter must be an object")
	}

	var filter map[string]json.RawMessage
	if err := json.Unmarshal(args[0], &filter); err != nil {
		return invalid("invalid eth_getLogs filter: %v", err)
	}
	for key := range filter {
		switch strings.ToLower(key) {
		case "fromblock":
			if key != "fromBlock" {
				return fmt.Errorf("%w: %w: %q", ErrInvalidRequest, ErrCaseVariantKey, key)
			}
		case "toblock":
			if key != "toBlock" {
				return fmt.Errorf("%w: %w: %q", ErrInvalidRequest, ErrCaseVariantKey, key)
			}
		}
	}

	fromRaw, hasFrom := filter["fromBlock"]
	toRaw, hasTo := filter["toBlock"]
	if !hasFrom && !hasTo {
		return nil
	}
	if hasFrom != hasTo {
		return unboundedRange("fromBlock and toBlock must both be present or both omitted")
	}

	from, fromIsNumber, fromTag, err := parseBlockRef(fromRaw)
	if err != nil {
		return err
	}
	to, toIsNumber, toTag, err := parseBlockRef(toRaw)
	if err != nil {
		return err
	}

	if !fromIsNumber || !toIsNumber {
		if fromTag != "" && fromTag == toTag {
			return nil
		}
		return unboundedRange("numeric fromBlock and toBlock are required to enforce the span limit")
	}
	if to < from {
		return invalid("toBlock is lower than fromBlock")
	}
	if to-from > g.maxGetLogsBlockSpan {
		return fmt.Errorf("%w: span %d exceeds limit %d", ErrBlockRangeTooLarge, to-from, g.maxGetLogsBlockSpan)
	}
	return nil
}

func parseBlockRef(raw json.RawMessage) (uint64, bool, string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false, "", invalid("block reference must be a string")
	}

	switch value {
	case "latest", "earliest", "pending", "safe", "finalized":
		return 0, false, value, nil
	}

	if len(value) < 3 || value[0] != '0' || value[1] != 'x' {
		return 0, false, "", invalid("invalid block quantity %q", value)
	}
	for _, digit := range value[2:] {
		if !((digit >= '0' && digit <= '9') || (digit >= 'a' && digit <= 'f') || (digit >= 'A' && digit <= 'F')) {
			return 0, false, "", invalid("invalid block quantity %q", value)
		}
	}
	number, err := strconv.ParseUint(value[2:], 16, 64)
	if err != nil {
		return 0, false, "", invalid("invalid block quantity %q", value)
	}
	return number, true, "", nil
}

func unboundedRange(reason string) error {
	return fmt.Errorf("%w: %w: %s", ErrBlockRangeTooLarge, ErrUnboundedBlockRange, reason)
}

func checkUniqueKeys(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := scanValue(decoder, 0, "$"); err != nil {
		return err
	}
	if _, err := decoder.Token(); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return invalid("malformed JSON: %v", err)
	}
	return invalid("trailing data after JSON value")
}

func scanValue(decoder *json.Decoder, depth int, path string) error {
	if depth > maxJSONDepth {
		return invalid("JSON nesting exceeds %d levels", maxJSONDepth)
	}

	token, err := decoder.Token()
	if err != nil {
		return invalid("malformed JSON: %v", err)
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
				return invalid("malformed object: %v", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return invalid("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("%w: %w: %s.%s", ErrInvalidRequest, ErrDuplicateKey, path, key)
			}
			seen[key] = struct{}{}
			if err := scanValue(decoder, depth+1, path+"."+key); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return invalid("unterminated object")
		}
	case '[':
		index := 0
		for decoder.More() {
			if err := scanValue(decoder, depth+1, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
			index++
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return invalid("unterminated array")
		}
	default:
		return invalid("unexpected JSON delimiter %q", delim)
	}
	return nil
}

func (g *Gate) ValidateRequest(r *http.Request) error {
	if r == nil || r.Body == nil {
		return invalid("missing request body")
	}

	body, readErr := io.ReadAll(io.LimitReader(r.Body, g.maxBodyBytes+1))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))

	if readErr != nil {
		return invalid("read request body: %v", readErr)
	}
	if int64(len(body)) > g.maxBodyBytes {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, ErrRequestTooLarge)
	}
	return g.Validate(body)
}

func (g *Gate) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := g.ValidateRequest(r); err != nil {
			writeRejection(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Gate) Handler(next http.Handler) http.Handler {
	return g.Middleware(next)
}

func writeRejection(w http.ResponseWriter, err error) {
	status, code, message := http.StatusBadRequest, -32600, "Invalid Request"
	switch {
	case errors.Is(err, ErrMethodNotAllowed):
		status, code, message = http.StatusForbidden, -32601, "Method not allowed"
	case errors.Is(err, ErrRequestTooLarge), errors.Is(err, ErrBatchTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrBlockRangeTooLarge):
		code, message = -32602, "Invalid params"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		JSONRPC string `json:"jsonrpc"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		ID any `json:"id"`
	}{
		JSONRPC: "2.0",
		Error: struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: code, Message: message},
		ID: nil,
	})
}

func isStructured(raw json.RawMessage) bool {
	first := firstNonSpace(raw)
	return first == '{' || first == '['
}

func firstNonSpace(raw []byte) byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return 0
	}
	return trimmed[0]
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, args...))
}
