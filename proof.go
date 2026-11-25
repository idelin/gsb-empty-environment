package displaycommit

import (
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

func nodeHash(left, right Hash) Hash {
	return DomainHash("displaycommit/merkle-node/v1", left[:], right[:])
}

func PageDigest(page Page) Hash {
	var lines [][]byte
	for _, l := range page.Lines {
		lines = append(lines, []byte(l.Label+"="+l.Text))
	}
	ref := make([][]byte, 0, len(page.References)+1)
	ref = append(ref, uint64Bytes(uint64(page.Index)))
	if page.PreviewPage != nil {
		ref = append(ref, int64Bytes(int64(*page.PreviewPage)))
	}
	for _, r := range page.References {
		ref = append(ref, int64Bytes(int64(r.Index)))
	}
	return DomainHash("displaycommit/page/v1", append(lines, ref...)...)
}

func LeafDigest(leaf Leaf) Hash {
	return DomainHash("displaycommit/leaf-id/v1", []byte(leaf.ID()))
}

// PageProof is a compact ordered-page Merkle inclusion proof.
type PageProof struct {
	Index    int    `json:"index"`
	LeafHash Hash   `json:"leaf_hash"`
	Siblings []Hash `json:"siblings"`
}

func BuildPageProof(pages []Page, index int) (PageProof, Hash, error) {
	if index < 0 || index >= len(pages) {
		return PageProof{}, ZeroHash, fmt.Errorf("displaycommit: page index %d out of range", index)
	}
	leaves := make([]Hash, len(pages))
	for i, p := range pages {
		p.Index = i
		leaves[i] = PageDigest(p)
	}
	proof := PageProof{Index: index, LeafHash: leaves[index]}
	idx := index
	level := append([]Hash(nil), leaves...)
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		sib := idx ^ 1
		proof.Siblings = append(proof.Siblings, level[sib])
		next := make([]Hash, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			next = append(next, nodeHash(level[i], level[i+1]))
		}
		idx /= 2
		level = next
	}
	root := level[0]
	return proof, root, nil
}

func (p PageProof) Verify(root Hash) bool {
	if p.LeafHash == ZeroHash {
		return false
	}
	h := p.LeafHash
	idx := p.Index
	for _, sib := range p.Siblings {
		if idx&1 == 0 {
			h = nodeHash(h, sib)
		} else {
			h = nodeHash(sib, h)
		}
		idx >>= 1
	}
	return h == root
}

// VerifyProof is a function-style alias for PageProof.Verify.
func VerifyProof(root Hash, proof PageProof) bool {
	return proof.Verify(root)
}

// HashTypeTable canonicalizes the declared normalized table. JSON map keys
// are sorted by encoding/json; the version is also bound separately.
func HashTypeTable(table *TypeTable) Hash {
	if table == nil {
		return ZeroHash
	}
	b, err := json.Marshal(table)
	if err != nil {
		return DomainHash("displaycommit/table-error/v1", []byte(table.versionValue()))
	}
	return DomainHash("displaycommit/type-table/v1", b)
}

// BuildLeafProof builds a compact inclusion proof for a message leaf.
func BuildLeafProof(msg *Message, path any) (PageProof, Hash, Leaf, error) {
	if msg == nil {
		return PageProof{}, ZeroHash, Leaf{}, fmt.Errorf("displaycommit: nil message")
	}
	leaves := msg.Leaves()
	want, err := NormalizePath(path)
	if err != nil {
		return PageProof{}, ZeroHash, Leaf{}, err
	}
	idx := -1
	for i, l := range leaves {
		if l.Path.Key() == want.Key() {
			idx = i
			break
		}
	}
	if idx < 0 {
		return PageProof{}, ZeroHash, Leaf{}, fmt.Errorf("displaycommit: leaf path %s not found", want.Key())
	}
	hashes := make([]Hash, len(leaves))
	for i, l := range leaves {
		hashes[i] = LeafDigest(l)
	}
	proof := PageProof{Index: idx, LeafHash: hashes[idx]}
	level := append([]Hash(nil), hashes...)
	cur := idx
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		sib := cur ^ 1
		proof.Siblings = append(proof.Siblings, level[sib])
		next := make([]Hash, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			next = append(next, nodeHash(level[i], level[i+1]))
		}
		cur /= 2
		level = next
	}
	return proof, level[0], leaves[idx], nil
}

// RequestBinding is the compact header a device retains with the current page.
type RequestBinding struct {
	TableVersion     string `json:"table_version"`
	TableRoot        Hash   `json:"table_root"`
	SessionNonce     string `json:"session_nonce"`
	MonotonicCounter uint64 `json:"monotonic_counter"`
	MessageRoot      Hash   `json:"message_root"`
	RawMessageRoot   Hash   `json:"raw_message_root"`
	PagesRoot        Hash   `json:"pages_root"`
	PageCount        int    `json:"page_count"`
}

func (b RequestBinding) Hash() Hash {
	return DomainHash("displaycommit/request-binding/v1",
		[]byte(b.TableVersion),
		b.TableRoot[:],
		[]byte(b.SessionNonce),
		uint64Bytes(b.MonotonicCounter),
		b.MessageRoot[:],
		b.RawMessageRoot[:],
		b.PagesRoot[:],
		uint64Bytes(uint64(b.PageCount)),
	)
}

// SigningRequest binds ordered screens, table version, session nonce/counter,
// and the raw message root.
type SigningRequest struct {
	Table   *TypeTable   `json:"-"`
	Rule    *DisplayRule `json:"-"`
	Message *Message     `json:"-"`
	Display *Display     `json:"display,omitempty"`

	TableVersion any `json:"table_version,omitempty"`
	Version      any `json:"version,omitempty"`

	SessionNonce     any    `json:"session_nonce,omitempty"`
	Nonce            uint64 `json:"nonce,omitempty"`
	DeviceNonce      uint64 `json:"device_nonce,omitempty"`
	MonotonicNonce   uint64 `json:"monotonic_nonce,omitempty"`
	MonotonicCounter uint64 `json:"monotonic_counter,omitempty"`
	Counter          uint64 `json:"counter,omitempty"`

	MessageRoot    Hash `json:"message_root"`
	RawMessageRoot Hash `json:"raw_message_root"`
	TableRoot      Hash `json:"table_root"`
	PagesRoot      Hash `json:"pages_root"`
	RequestHash    Hash `json:"request_hash"`

	Pages []Page `json:"pages,omitempty"`
}

func anyScalarString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return "bytes:" + fmt.Sprintf("%x", x)
	case Hash:
		return x.String()
	case uint64:
		return fmt.Sprintf("%d", x)
	case int:
		return fmt.Sprintf("%d", x)
	default:
		return fmt.Sprint(x)
	}
}

func (r *SigningRequest) tableVersion() string {
	if r.TableVersion != nil {
		if s := anyScalarString(r.TableVersion); s != "" {
			return s
		}
	}
	if r.Version != nil {
		if s := anyScalarString(r.Version); s != "" {
			return s
		}
	}
	if r.Table != nil {
		return r.Table.versionValue()
	}
	return "unversioned-table"
}

func (r *SigningRequest) nonceString() string {
	if r.SessionNonce != nil {
		return anyScalarString(r.SessionNonce)
	}
	for _, n := range []uint64{r.Nonce, r.DeviceNonce, r.MonotonicNonce} {
		if n != 0 {
			return fmt.Sprintf("%d", n)
		}
	}
	return ""
}

func (r *SigningRequest) counterValue() uint64 {
	for _, n := range []uint64{r.MonotonicCounter, r.Counter, r.MonotonicNonce} {
		if n != 0 {
			return n
		}
	}
	return 0
}

func (r *SigningRequest) Binding() RequestBinding {
	msgRoot := r.MessageRoot
	rawRoot := r.RawMessageRoot
	tableRoot := r.TableRoot
	if tableRoot.IsZero() && r.Table != nil {
		tableRoot = HashTypeTable(r.Table)
	}
	if r.Message != nil {
		if msgRoot.IsZero() {
			msgRoot = r.Message.rootOrComputed()
		}
		if rawRoot.IsZero() {
			rawRoot = r.Message.ComputeRoot()
		}
	}
	pages := r.Pages
	if len(pages) == 0 && r.Display != nil {
		pages = r.Display.Pages
	}
	pagesRoot := r.PagesRoot
	if pagesRoot.IsZero() && len(pages) > 0 {
		hashes := make([]Hash, len(pages))
		for i, p := range pages {
			p.Index = i
			hashes[i] = PageDigest(p)
		}
		root, err := merkleRootHashes(hashes)
		if err == nil {
			pagesRoot = root
		}
	}
	return RequestBinding{
		TableVersion:     r.tableVersion(),
		TableRoot:        tableRoot,
		SessionNonce:     r.nonceString(),
		MonotonicCounter: r.counterValue(),
		MessageRoot:      msgRoot,
		RawMessageRoot:   rawRoot,
		PagesRoot:        pagesRoot,
		PageCount:        len(pages),
	}
}

func merkleRootHashes(leaves []Hash) (Hash, error) {
	if len(leaves) == 0 {
		return ZeroHash, fmt.Errorf("empty pages")
	}
	level := append([]Hash(nil), leaves...)
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		next := make([]Hash, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			next = append(next, nodeHash(level[i], level[i+1]))
		}
		level = next
	}
	return level[0], nil
}

func (r *SigningRequest) ComputeRequestHash() Hash {
	b := r.Binding()
	return b.Hash()
}

// Commit seals roots/pages and returns the request commitment hash.
func (r *SigningRequest) Commit() (Hash, error) {
	if _, err := r.Seal(); err != nil {
		return ZeroHash, err
	}
	return r.RequestHash, nil
}

// Digest recomputes the canonical request hash without mutating the request.
func (r *SigningRequest) Digest() Hash {
	return r.ComputeRequestHash()
}

// Seal computes and stores roots and the request hash.
func (r *SigningRequest) Seal() (*SigningRequest, error) {
	if r.Message == nil {
		return nil, fmt.Errorf("displaycommit: signing request needs message")
	}
	if r.Display == nil {
		d, err := Render(r.Table, r.Message, r.Rule)
		if err != nil {
			return nil, err
		}
		r.Display = d
	}
	r.Pages = append([]Page(nil), r.Display.Pages...)
	r.MessageRoot = r.Message.rootOrComputed()
	r.RawMessageRoot = r.Message.ComputeRoot()
	r.TableRoot = HashTypeTable(r.Table)
	hashes := make([]Hash, len(r.Pages))
	for i := range r.Pages {
		proof, root, err := BuildPageProof(r.Pages, i)
		if err != nil {
			return nil, err
		}
		r.PagesRoot = root
		if r.Display != nil && i < len(r.Display.Pages) {
			r.Display.Pages[i].Proof = &proof
		}
		hashes[i] = proof.LeafHash
	}
	if r.PagesRoot.IsZero() && len(hashes) > 0 {
		root, err := merkleRootHashes(hashes)
		if err != nil {
			return nil, err
		}
		r.PagesRoot = root
	}
	r.RequestHash = r.ComputeRequestHash()
	return r, nil
}

func NewSigningRequest(table *TypeTable, rule *DisplayRule, msg *Message, nonce ...any) (*SigningRequest, error) {
	r := &SigningRequest{Table: table, Rule: rule, Message: msg}
	if table != nil {
		r.TableVersion = table.Version
	}
	if len(nonce) > 0 {
		r.SessionNonce = nonce[0]
		if n, ok := nonce[0].(uint64); ok {
			r.MonotonicCounter = n
		}
	}
	return r.Seal()
}

// CreateSigningRequest accepts the common argument orders used by compact
// callers and tests: table, rule, message and an optional nonce.
func CreateSigningRequest(args ...any) (*SigningRequest, error) {
	r := &SigningRequest{}
	var nonce any
	for _, arg := range args {
		switch x := arg.(type) {
		case *TypeTable:
			r.Table = x
		case TypeTable:
			r.Table = &x
		case *DisplayRule:
			r.Rule = x
		case DisplayRule:
			r.Rule = &x
		case *Message:
			r.Message = x
		case Message:
			r.Message = &x
		case Hash:
			if r.MessageRoot.IsZero() {
				r.MessageRoot = x
			}
		case uint64, int, int64, string, []byte:
			nonce = x
		}
	}
	r.SessionNonce = nonce
	if n, ok := nonce.(uint64); ok {
		r.MonotonicCounter = n
	}
	if r.Table != nil {
		r.TableVersion = r.Table.Version
	}
	return r.Seal()
}

func (r *SigningRequest) Sign(key ed25519.PrivateKey) []byte {
	h := r.ComputeRequestHash()
	return ed25519.Sign(key, h[:])
}

func VerifyRequestSignature(pub ed25519.PublicKey, r *SigningRequest, sig []byte) bool {
	h := r.ComputeRequestHash()
	return ed25519.Verify(pub, h[:], sig)
}

// PagePacket is the current page plus compact proof and request header.
type PagePacket struct {
	Binding RequestBinding `json:"binding"`
	Page    Page           `json:"page"`
	Proof   PageProof      `json:"proof"`
}

func (r *SigningRequest) Packet(pageIndex int) (*PagePacket, error) {
	if pageIndex < 0 || pageIndex >= len(r.Pages) {
		return nil, fmt.Errorf("displaycommit: page %d unavailable", pageIndex)
	}
	proof, root, err := BuildPageProof(r.Pages, pageIndex)
	if err != nil {
		return nil, err
	}
	b := r.Binding()
	b.PagesRoot = root
	return &PagePacket{Binding: b, Page: r.Pages[pageIndex], Proof: proof}, nil
}

// VerifyAllPageProofs checks every attached page proof against PagesRoot.
func (r *SigningRequest) VerifyAllPageProofs() bool {
	if r == nil || r.PagesRoot == ZeroHash {
		return false
	}
	pages := r.Pages
	if len(pages) == 0 && r.Display != nil {
		pages = r.Display.Pages
	}
	if len(pages) == 0 {
		return false
	}
	for i, p := range pages {
		if p.Proof == nil {
			proof, _, err := BuildPageProof(pages, i)
			if err != nil || !proof.Verify(r.PagesRoot) || PageDigest(p) != proof.LeafHash {
				return false
			}
			continue
		}
		if p.Proof.Index != i || !p.Proof.Verify(r.PagesRoot) || PageDigest(p) != p.Proof.LeafHash {
			return false
		}
	}
	return r.RequestHash == r.ComputeRequestHash()
}

// Verify checks the page proof and reconstructs the same request hash. A
// device compares Packet.Binding.Hash()/request hash to its trusted value.
func (p *PagePacket) Verify() (Hash, bool) {
	if p == nil {
		return ZeroHash, false
	}
	if !p.Proof.Verify(p.Binding.PagesRoot) {
		return ZeroHash, false
	}
	if PageDigest(p.Page) != p.Proof.LeafHash {
		return ZeroHash, false
	}
	return p.Binding.Hash(), true
}

var _ = binary.BigEndian
