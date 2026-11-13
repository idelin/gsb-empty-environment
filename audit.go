package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"
)

// ErrAuditCallbackPanic is returned when a pending cacheKey callback panics
// during the differential search.
var ErrAuditCallbackPanic = errors.New("netsec: audit cacheKey callback panicked")

// ErrAuditLimit is returned when the bounded search reaches MaxCases without
// completing. Increase MaxCases or reduce the token search space.
var ErrAuditLimit = errors.New("netsec: weak-key audit case limit exceeded")

// AuditRequest is one synthetic request used to review a cacheKey callback.
// SystemPromptDigest is folded into the canonical token-prefix digest, which
// models a host whose prompt includes a private system message.
type AuditRequest struct {
	TenantID               string
	ModelDigest            string
	AdapterDigest          string
	TokenizerDigest        string
	DecodePosition         int64
	Tokens                 []Token
	SystemPromptDigest     [32]byte
	SafetyPolicyGeneration uint64
	KVLayoutVersion        uint32
	PublicScope            string
	SecretToken            Token
}

// Identity returns the canonical identity that the real cache would require.
func (r AuditRequest) Identity() Identity {
	tenant := r.TenantID
	if r.PublicScope != "" {
		tenant = ""
	}
	return Identity{
		TenantID:               tenant,
		ModelDigest:            r.ModelDigest,
		AdapterDigest:          r.AdapterDigest,
		TokenizerDigest:        r.TokenizerDigest,
		DecodePosition:         r.DecodePosition,
		TokenPrefixDigest:      CanonicalTokenPrefixDigest(r.Tokens, r.SystemPromptDigest),
		SafetyPolicyGeneration: r.SafetyPolicyGeneration,
		KVLayoutVersion:        r.KVLayoutVersion,
		PublicScope:            r.PublicScope,
	}
}

// WeakKeyCallback is the callback under review. It is intentionally allowed to
// be as weak as the candidate implementation; the audit supplies the canonical
// identity separately when checking a collision.
type WeakKeyCallback func(AuditRequest) string

// CacheKeyCallback is an alias for WeakKeyCallback.
type CacheKeyCallback = WeakKeyCallback

// TokenOnlyWeakKey is the vulnerable key shape from the motivating incident:
// it hashes only the token ids and ignores tenant, prompt, weights, adapter,
// tokenizer, policy generation, position, layout, and public scope.
func TokenOnlyWeakKey(r AuditRequest) string {
	var b strings.Builder
	b.Grow(len(r.Tokens) * 8)
	for _, tok := range r.Tokens {
		b.WriteByte(byte(tok))
		b.WriteByte(byte(tok >> 8))
		b.WriteByte(byte(tok >> 16))
		b.WriteByte(byte(tok >> 24))
	}
	return b.String()
}

// SafeAuditKey derives a key from the complete canonical identity.
func SafeAuditKey(r AuditRequest) string {
	return string(r.Identity().Canonical())
}

// MarkedKV is the decoded form of the synthetic KV payload used by the audit.
type MarkedKV struct {
	TenantID               string
	ModelDigest            string
	AdapterDigest          string
	TokenizerDigest        string
	DecodePosition         int64
	Tokens                 []Token
	SystemPromptDigest     [32]byte
	SafetyPolicyGeneration uint64
	KVLayoutVersion        uint32
	PublicScope            string
	SecretToken            Token
}

var markedKVMagic = []byte("KVPCMARK1")

// EncodeMarkedKV serializes a request's secret marker into synthetic KV bytes.
// The audit uses this to prove that a weak-key hit returns the writer's KV,
// not merely that two keys happened to be equal.
func EncodeMarkedKV(r AuditRequest) []byte {
	buf := make([]byte, 0, 256)
	buf = append(buf, markedKVMagic...)
	buf = appendMarkedString(buf, r.TenantID)
	buf = appendMarkedString(buf, r.ModelDigest)
	buf = appendMarkedString(buf, r.AdapterDigest)
	buf = appendMarkedString(buf, r.TokenizerDigest)
	buf = appendUint64(buf, uint64(r.DecodePosition))
	buf = appendUint32(buf, uint32(len(r.Tokens)))
	for _, tok := range r.Tokens {
		buf = appendUint32(buf, uint32(tok))
	}
	buf = append(buf, r.SystemPromptDigest[:]...)
	buf = appendUint64(buf, r.SafetyPolicyGeneration)
	buf = appendUint32(buf, r.KVLayoutVersion)
	buf = appendMarkedString(buf, r.PublicScope)
	buf = appendUint32(buf, uint32(r.SecretToken))
	return buf
}

func appendMarkedString(dst []byte, s string) []byte {
	dst = appendUint32(dst, uint32(len(s)))
	return append(dst, s...)
}

type markedReader struct {
	b   []byte
	off int
}

func (r *markedReader) bytes(n int) ([]byte, error) {
	if n < 0 || r.off > len(r.b)-n {
		return nil, errors.New("netsec: truncated marked KV")
	}
	out := r.b[r.off : r.off+n]
	r.off += n
	return out, nil
}

func (r *markedReader) u32() (uint32, error) {
	b, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (r *markedReader) u64() (uint64, error) {
	b, err := r.bytes(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b), nil
}

func (r *markedReader) str() (string, error) {
	n, err := r.u32()
	if err != nil {
		return "", err
	}
	if uint64(n) > uint64(len(r.b)-r.off) {
		return "", errors.New("netsec: truncated marked KV string")
	}
	b, err := r.bytes(int(n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DecodeMarkedKV decodes the synthetic KV marker.
func DecodeMarkedKV(data []byte) (MarkedKV, error) {
	var out MarkedKV
	if len(data) < len(markedKVMagic) || !bytes.Equal(data[:len(markedKVMagic)], markedKVMagic) {
		return out, errors.New("netsec: invalid marked KV magic")
	}
	r := &markedReader{b: data, off: len(markedKVMagic)}
	var err error
	if out.TenantID, err = r.str(); err != nil {
		return out, err
	}
	if out.ModelDigest, err = r.str(); err != nil {
		return out, err
	}
	if out.AdapterDigest, err = r.str(); err != nil {
		return out, err
	}
	if out.TokenizerDigest, err = r.str(); err != nil {
		return out, err
	}
	pos, err := r.u64()
	if err != nil {
		return out, err
	}
	out.DecodePosition = int64(pos)
	n, err := r.u32()
	if err != nil {
		return out, err
	}
	if uint64(n) > uint64(len(data)-r.off)/4 {
		return out, errors.New("netsec: truncated marked KV token list")
	}
	out.Tokens = make([]Token, n)
	for i := range out.Tokens {
		v, err := r.u32()
		if err != nil {
			return out, err
		}
		out.Tokens[i] = Token(v)
	}
	prompt, err := r.bytes(32)
	if err != nil {
		return out, err
	}
	copy(out.SystemPromptDigest[:], prompt)
	if out.SafetyPolicyGeneration, err = r.u64(); err != nil {
		return out, err
	}
	if out.KVLayoutVersion, err = r.u32(); err != nil {
		return out, err
	}
	if out.PublicScope, err = r.str(); err != nil {
		return out, err
	}
	secret, err := r.u32()
	if err != nil {
		return out, err
	}
	out.SecretToken = Token(secret)
	if r.off != len(data) {
		return out, errors.New("netsec: trailing bytes in marked KV")
	}
	return out, nil
}

// AuditOptions bounds the finite-difference search.
type AuditOptions struct {
	Tenants    []string
	Alphabet   []Token
	MaxTokens  int
	MaxCases   int
	SeedTokens [][]Token
	// AllowSameTenant permits findings where the writer and reader are the
	// same tenant. The default is false because the incident is cross-tenant.
	AllowSameTenant bool
}

func (o AuditOptions) withDefaults() AuditOptions {
	if len(o.Tenants) == 0 {
		o.Tenants = []string{"tenant-a", "tenant-b"}
	}
	if len(o.Alphabet) == 0 {
		o.Alphabet = []Token{1, 2, 3}
	}
	if o.MaxTokens == 0 {
		o.MaxTokens = 4
	}
	if o.MaxCases == 0 {
		o.MaxCases = 100000
	}
	if o.MaxTokens < 1 {
		o.MaxTokens = 1
	}
	if o.MaxCases < 1 {
		o.MaxCases = 1
	}
	return o
}

// AuditFinding is the shortest weak-key collision found by the search.
type AuditFinding struct {
	WeakKey         string
	Writer          AuditRequest
	Reader          AuditRequest
	LeakedToken     Token
	MarkedKV        []byte
	TokenLength     int
	TotalTokens     int
	DifferenceCount int
	Reasons         []string
}

// Reason returns a human-readable summary of the identity dimensions that the
// weak key failed to bind.
func (f *AuditFinding) Reason() string {
	if f == nil {
		return ""
	}
	if len(f.Reasons) == 0 {
		return "weak cache key collides across distinct canonical identities"
	}
	return strings.Join(f.Reasons, "; ")
}

type auditDimension struct {
	name     string
	variants []func(*AuditRequest)
}

func promptDigest(label string) [32]byte {
	return sha256.Sum256([]byte("netsec.audit.prompt\x00" + label))
}

func auditDimensions(tenant string) []auditDimension {
	return []auditDimension{
		{name: "model", variants: []func(*AuditRequest){
			func(*AuditRequest) {},
			func(r *AuditRequest) { r.ModelDigest = "model:weights:quant-int8" },
		}},
		{name: "adapter", variants: []func(*AuditRequest){
			func(*AuditRequest) {},
			func(r *AuditRequest) { r.AdapterDigest = "adapter:lora:finance" },
		}},
		{name: "tokenizer", variants: []func(*AuditRequest){
			func(*AuditRequest) {},
			func(r *AuditRequest) { r.TokenizerDigest = "tokenizer:v2" },
		}},
		{name: "position", variants: []func(*AuditRequest){
			func(*AuditRequest) {},
			func(r *AuditRequest) { r.DecodePosition = 1 },
		}},
		{name: "system-prompt", variants: []func(*AuditRequest){
			func(*AuditRequest) {},
			func(r *AuditRequest) { r.SystemPromptDigest = promptDigest("system:public") },
			func(r *AuditRequest) { r.SystemPromptDigest = promptDigest("system:private:" + tenant) },
		}},
		{name: "policy-generation", variants: []func(*AuditRequest){
			func(*AuditRequest) {},
			func(r *AuditRequest) { r.SafetyPolicyGeneration = 2 },
		}},
		{name: "kv-layout", variants: []func(*AuditRequest){
			func(*AuditRequest) {},
			func(r *AuditRequest) { r.KVLayoutVersion = 2 },
		}},
		{name: "public-scope", variants: []func(*AuditRequest){
			func(*AuditRequest) {},
			func(r *AuditRequest) { r.PublicScope = "public:shared-context" },
		}},
	}
}

func auditCases(tokens []Token, opts AuditOptions) []AuditRequest {
	base := AuditRequest{
		ModelDigest:            "model:weights:fp16",
		AdapterDigest:          "",
		TokenizerDigest:        "tokenizer:v1",
		DecodePosition:         0,
		Tokens:                 append([]Token(nil), tokens...),
		SystemPromptDigest:     promptDigest("system:public"),
		SafetyPolicyGeneration: 1,
		KVLayoutVersion:        1,
		PublicScope:            "",
	}
	var out []AuditRequest
	seen := make(map[string]struct{})
	add := func(r AuditRequest) {
		key := string(r.Identity().Canonical())
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	for i, tenant := range opts.Tenants {
		t := base
		t.TenantID = tenant
		t.SecretToken = Token(1000 + i)
		t.SystemPromptDigest = promptDigest("system:private:" + tenant)
		add(t)
		dims := auditDimensions(tenant)
		// First-order finite differences.
		for _, dim := range dims {
			for _, apply := range dim.variants {
				c := t
				c.Tokens = append([]Token(nil), t.Tokens...)
				apply(&c)
				add(c)
			}
		}
		// Second-order differences find keys that omit two dimensions, such as
		// tenant and system prompt, or model and adapter.
		for x := 0; x < len(dims); x++ {
			for y := x + 1; y < len(dims); y++ {
				for _, ax := range dims[x].variants {
					for _, ay := range dims[y].variants {
						c := t
						c.Tokens = append([]Token(nil), t.Tokens...)
						ax(&c)
						ay(&c)
						add(c)
					}
				}
			}
		}
	}
	return out
}

// forEachSequence visits every length-token sequence deterministically and
// stops as soon as fn returns false. It avoids materializing the (potentially
// exponential) sequence space before the case limit is applied.
func forEachSequence(alphabet []Token, length int, fn func([]Token) bool) {
	if length <= 0 || len(alphabet) == 0 {
		return
	}
	cur := make([]Token, 0, length)
	var walk func() bool
	walk = func() bool {
		if len(cur) == length {
			return fn(cur)
		}
		for _, tok := range alphabet {
			cur = append(cur, tok)
			if !walk() {
				return false
			}
			cur = cur[:len(cur)-1]
		}
		return true
	}
	walk()
}

func callWeakKey(cb WeakKeyCallback, r AuditRequest) (key string, err error) {
	defer func() {
		if recover() != nil {
			err = ErrAuditCallbackPanic
		}
	}()
	return cb(r), nil
}

func differingFields(a, b AuditRequest) []string {
	var reasons []string
	if a.TenantID != b.TenantID {
		reasons = append(reasons, "tenant id")
	}
	if a.ModelDigest != b.ModelDigest {
		reasons = append(reasons, "model/quantization digest")
	}
	if a.AdapterDigest != b.AdapterDigest {
		reasons = append(reasons, "adapter digest")
	}
	if a.TokenizerDigest != b.TokenizerDigest {
		reasons = append(reasons, "tokenizer digest")
	}
	if a.DecodePosition != b.DecodePosition {
		reasons = append(reasons, "decode position")
	}
	if a.SystemPromptDigest != b.SystemPromptDigest {
		reasons = append(reasons, "system prompt digest")
	}
	if !tokensEqual(a.Tokens, b.Tokens) {
		reasons = append(reasons, "token prefix")
	}
	if a.SafetyPolicyGeneration != b.SafetyPolicyGeneration {
		reasons = append(reasons, "safety policy generation")
	}
	if a.KVLayoutVersion != b.KVLayoutVersion {
		reasons = append(reasons, "KV layout version")
	}
	if a.PublicScope != b.PublicScope {
		reasons = append(reasons, "public scope")
	}
	return reasons
}

func tokensEqual(a, b []Token) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func leakFinding(weakKey string, writer, reader AuditRequest, opts AuditOptions) *AuditFinding {
	if opts.AllowSameTenant == false && writer.TenantID == reader.TenantID {
		return nil
	}
	if bytes.Equal(writer.Identity().Canonical(), reader.Identity().Canonical()) {
		return nil
	}
	marked := EncodeMarkedKV(writer)
	decoded, err := DecodeMarkedKV(marked)
	if err != nil {
		return nil
	}
	if opts.AllowSameTenant == false && decoded.TenantID == reader.TenantID {
		return nil
	}
	reasons := differingFields(writer, reader)
	return &AuditFinding{
		WeakKey:         weakKey,
		Writer:          writer,
		Reader:          reader,
		LeakedToken:     decoded.SecretToken,
		MarkedKV:        marked,
		TokenLength:     maxInt(len(writer.Tokens), len(reader.Tokens)),
		TotalTokens:     len(writer.Tokens) + len(reader.Tokens),
		DifferenceCount: len(reasons),
		Reasons:         reasons,
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func betterFinding(a, b *AuditFinding) bool {
	if a == nil {
		return false
	}
	if b == nil {
		return true
	}
	if a.TokenLength != b.TokenLength {
		return a.TokenLength < b.TokenLength
	}
	// A same-token/different-system-prompt collision is the direct analogue of
	// the reported incident, so prefer it when token lengths tie.
	if promptDiff := a.Writer.SystemPromptDigest != a.Reader.SystemPromptDigest; promptDiff {
		if b.Writer.SystemPromptDigest == b.Reader.SystemPromptDigest {
			return true
		}
	} else if b.Writer.SystemPromptDigest != b.Reader.SystemPromptDigest {
		return false
	}
	if a.DifferenceCount != b.DifferenceCount {
		return a.DifferenceCount < b.DifferenceCount
	}
	if a.TotalTokens != b.TotalTokens {
		return a.TotalTokens < b.TotalTokens
	}
	if a.WeakKey != b.WeakKey {
		return a.WeakKey < b.WeakKey
	}
	return bytes.Compare(a.Writer.Identity().Canonical(), b.Writer.Identity().Canonical()) < 0
}

// SearchWeakCacheKey performs a bounded, deterministic finite-difference
// search over token lengths and identity coordinates. At the shortest token
// length where a collision exists it returns the finding with the fewest
// differing identity dimensions.
//
// A finding means: the candidate callback returned the same weak key for two
// requests whose canonical identities differ, the requests belong to different
// tenants (unless AllowSameTenant is set), and the marked KV written by the
// first request decodes to the second tenant's visible secret token.
func SearchWeakCacheKey(cb WeakKeyCallback, opts AuditOptions) (*AuditFinding, error) {
	if cb == nil {
		return nil, errors.New("netsec: nil cacheKey callback")
	}
	opts = opts.withDefaults()
	totalCases := 0
	groups := make(map[string][]AuditRequest)
	order := make([]string, 0)
	seedsByLength := make(map[int][][]Token)
	maxLength := opts.MaxTokens
	for _, seed := range opts.SeedTokens {
		if len(seed) == 0 {
			continue
		}
		seedsByLength[len(seed)] = append(seedsByLength[len(seed)], append([]Token(nil), seed...))
		if len(seed) > maxLength {
			maxLength = len(seed)
		}
	}
	for length := 1; length <= maxLength; length++ {
		limitHit := false
		var auditErr error
		process := func(tokens []Token) bool {
			cases := auditCases(tokens, opts)
			totalCases += len(cases)
			if totalCases > opts.MaxCases {
				limitHit = true
				return false
			}
			for _, c := range cases {
				key, err := callWeakKey(cb, c)
				if err != nil {
					auditErr = err
					return false
				}
				if _, ok := groups[key]; !ok {
					order = append(order, key)
				}
				groups[key] = append(groups[key], c)
			}
			return true
		}
		forEachSequence(opts.Alphabet, length, process)
		if auditErr != nil {
			return nil, auditErr
		}
		for _, seed := range seedsByLength[length] {
			if !process(seed) {
				break
			}
		}
		if auditErr != nil {
			return nil, auditErr
		}
		if limitHit {
			return nil, ErrAuditLimit
		}
		best := bestInGroups(groups, order, opts)
		if best != nil {
			return best, nil
		}
	}
	return nil, nil
}

// bestInGroups compares every same-weak-key pair seen up to the current token
// length. Keeping earlier-length groups lets the search catch ambiguous weak
// encodings that collide across otherwise different token sequences.
func bestInGroups(groups map[string][]AuditRequest, order []string, opts AuditOptions) *AuditFinding {
	var best *AuditFinding
	for _, key := range order {
		group := groups[key]
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				// Try both write orders. The first request into the weak-key
				// slot owns the KV that the other request sees.
				if f := leakFinding(key, group[i], group[j], opts); f != nil && betterFinding(f, best) {
					best = f
				}
				if f := leakFinding(key, group[j], group[i], opts); f != nil && betterFinding(f, best) {
					best = f
				}
			}
		}
	}
	return best
}

// AuditCacheKey is an alias for SearchWeakCacheKey.
func AuditCacheKey(cb CacheKeyCallback, opts AuditOptions) (*AuditFinding, error) {
	return SearchWeakCacheKey(cb, opts)
}

// FiniteDifferenceSearch is an alias for SearchWeakCacheKey.
func FiniteDifferenceSearch(cb CacheKeyCallback, opts AuditOptions) (*AuditFinding, error) {
	return SearchWeakCacheKey(cb, opts)
}
