package netsec

import (
	"crypto/sha256"
	"fmt"
)

// HeaderField is one HTTP header field decoded from an HPACK block.
type HeaderField struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

// IsPseudo reports whether the field name is a pseudo-header.
func (f HeaderField) IsPseudo() bool { return len(f.Name) > 0 && f.Name[0] == ':' }

// Size returns the RFC 7541 section 4.1 dynamic table entry size.
func (f HeaderField) Size() uint32 {
	return uint32(len(f.Name) + len(f.Value) + 32)
}

// HeaderListSize returns the RFC 7540 section 6.5.2 header list size.
func (f HeaderField) HeaderListSize() int { return len(f.Name) + len(f.Value) + 32 }

var staticTable = [...]HeaderField{
	{Name: ":authority"},
	{Name: ":method", Value: "GET"},
	{Name: ":method", Value: "POST"},
	{Name: ":path", Value: "/"},
	{Name: ":path", Value: "/index.html"},
	{Name: ":scheme", Value: "http"},
	{Name: ":scheme", Value: "https"},
	{Name: ":status", Value: "200"},
	{Name: ":status", Value: "204"},
	{Name: ":status", Value: "206"},
	{Name: ":status", Value: "304"},
	{Name: ":status", Value: "400"},
	{Name: ":status", Value: "404"},
	{Name: ":status", Value: "500"},
	{Name: "accept-charset"},
	{Name: "accept-encoding", Value: "gzip, deflate"},
	{Name: "accept-language"},
	{Name: "accept-ranges"},
	{Name: "accept"},
	{Name: "access-control-allow-origin"},
	{Name: "age"},
	{Name: "allow"},
	{Name: "authorization"},
	{Name: "cache-control"},
	{Name: "content-disposition"},
	{Name: "content-encoding"},
	{Name: "content-language"},
	{Name: "content-length"},
	{Name: "content-location"},
	{Name: "content-range"},
	{Name: "content-type"},
	{Name: "cookie"},
	{Name: "date"},
	{Name: "etag"},
	{Name: "expect"},
	{Name: "expires"},
	{Name: "from"},
	{Name: "host"},
	{Name: "if-match"},
	{Name: "if-modified-since"},
	{Name: "if-none-match"},
	{Name: "if-range"},
	{Name: "if-unmodified-since"},
	{Name: "last-modified"},
	{Name: "link"},
	{Name: "location"},
	{Name: "max-forwards"},
	{Name: "proxy-authenticate"},
	{Name: "proxy-authorization"},
	{Name: "range"},
	{Name: "referer"},
	{Name: "refresh"},
	{Name: "retry-after"},
	{Name: "server"},
	{Name: "set-cookie"},
	{Name: "strict-transport-security"},
	{Name: "transfer-encoding"},
	{Name: "user-agent"},
	{Name: "vary"},
	{Name: "via"},
	{Name: "www-authenticate"},
}

const staticTableLen = uint64(len(staticTable))

// DynamicTable is an HPACK dynamic table. Entries are stored newest first,
// matching HPACK index order after the static table.
type DynamicTable struct {
	entries        []HeaderField
	size           uint32
	maxSize        uint32
	allowedMaxSize uint32
}

// NewDynamicTable returns a dynamic table whose current and allowed maximum
// size are both maxSize.
func NewDynamicTable(maxSize uint32) *DynamicTable {
	return &DynamicTable{maxSize: maxSize, allowedMaxSize: maxSize}
}

// Clone returns an independent copy.
func (t *DynamicTable) Clone() *DynamicTable {
	if t == nil {
		return nil
	}
	c := &DynamicTable{
		entries:        append([]HeaderField(nil), t.entries...),
		size:           t.size,
		maxSize:        t.maxSize,
		allowedMaxSize: t.allowedMaxSize,
	}
	return c
}

// Entries returns the dynamic entries newest first.
func (t *DynamicTable) Entries() []HeaderField {
	return append([]HeaderField(nil), t.entries...)
}

// Len returns the number of dynamic entries.
func (t *DynamicTable) Len() int { return len(t.entries) }

// Size returns the current dynamic table size in HPACK bytes.
func (t *DynamicTable) Size() uint32 { return t.size }

// MaxSize returns the current maximum dynamic table size.
func (t *DynamicTable) MaxSize() uint32 { return t.maxSize }

// AllowedMaxSize returns the upper bound on dynamic table size updates.
func (t *DynamicTable) AllowedMaxSize() uint32 { return t.allowedMaxSize }

// SetAllowedMaxSize changes the upper bound for dynamic table size updates.
// If the current maximum exceeds the new bound it is lowered and entries are
// evicted.
func (t *DynamicTable) SetAllowedMaxSize(v uint32) {
	t.allowedMaxSize = v
	if t.maxSize > v {
		t.setMaxSize(v)
	}
}

// SetMaxSize applies a dynamic table size update.
func (t *DynamicTable) SetMaxSize(v uint32) error {
	if v > t.allowedMaxSize {
		return fmt.Errorf("%w: %d > allowed %d", ErrDynamicTableSize, v, t.allowedMaxSize)
	}
	t.setMaxSize(v)
	return nil
}

func (t *DynamicTable) setMaxSize(v uint32) []HeaderField {
	t.maxSize = v
	return t.evictToFit()
}

func (t *DynamicTable) evictToFit() []HeaderField {
	if t.size <= t.maxSize {
		return nil
	}
	var evicted []HeaderField
	for t.size > t.maxSize && len(t.entries) > 0 {
		oldest := t.entries[len(t.entries)-1]
		evicted = append(evicted, oldest)
		t.size -= oldest.Size()
		t.entries = t.entries[:len(t.entries)-1]
	}
	return evicted
}

func (t *DynamicTable) evictAll() []HeaderField {
	if len(t.entries) == 0 {
		t.size = 0
		return nil
	}
	evicted := append([]HeaderField(nil), t.entries...)
	t.entries = t.entries[:0]
	t.size = 0
	return evicted
}

// Add inserts f, evicting older entries as needed. An entry larger than the
// current maximum size empties the table and is not inserted, per RFC 7541
// section 4.4.
func (t *DynamicTable) Add(f HeaderField) []HeaderField {
	entrySize := f.Size()
	if entrySize > t.maxSize {
		return t.evictAll()
	}
	t.entries = append(t.entries, HeaderField{})
	copy(t.entries[1:], t.entries[:len(t.entries)-1])
	t.entries[0] = f
	t.size += entrySize
	return t.evictToFit()
}

func (t *DynamicTable) at(index uint64) (HeaderField, bool) {
	if index == 0 || index > uint64(len(t.entries)) {
		return HeaderField{}, false
	}
	return t.entries[index-1], true
}

// Lookup returns a dynamic entry by HPACK dynamic-table index, where 1 is the
// newest entry (index 62 overall with the standard static table).
func (t *DynamicTable) Lookup(index uint64) (HeaderField, bool) { return t.at(index) }

func tableAt(index uint64) (HeaderField, bool) {
	if index == 0 {
		return HeaderField{}, false
	}
	if index <= staticTableLen {
		f := staticTable[index-1]
		return f, true
	}
	return HeaderField{}, false
}

// TableOpKind identifies one replayable dynamic table mutation.
type TableOpKind uint8

const (
	TableOpSetMaxSize TableOpKind = 1
	TableOpInsert     TableOpKind = 2
)

func (k TableOpKind) String() string {
	switch k {
	case TableOpSetMaxSize:
		return "set_max_size"
	case TableOpInsert:
		return "insert"
	default:
		return "unknown"
	}
}

// TableOp is one replayable operation from a committed header block.
type TableOp struct {
	Kind    TableOpKind `json:"kind"`
	MaxSize uint32      `json:"maxSize,omitempty"`
	Field   HeaderField `json:"field,omitempty"`
}

// TableCommit describes a fully validated header block and the exact dynamic
// table changes it committed. Replaying Operations onto a DynamicTable that
// started from InitialMaxSize reproduces the decoder's table state. Fields is
// an audit copy in which sensitive values are replaced with "<redacted>".
type TableCommit struct {
	Sequence           uint64        `json:"sequence"`
	BlockID            uint64        `json:"blockId"`
	StreamID           uint32        `json:"streamId"`
	PeerID             string        `json:"peerId,omitempty"`
	TenantID           string        `json:"tenantId,omitempty"`
	SettingsGeneration uint64        `json:"settingsGeneration"`
	CompressedBytes    int           `json:"compressedBytes"`
	DecompressedBytes  int           `json:"decompressedBytes"`
	FieldCount         int           `json:"fieldCount"`
	Digest             [32]byte      `json:"digest"`
	InitialMaxSize     uint32        `json:"initialMaxSize"`
	AllowedMaxSize     uint32        `json:"allowedMaxSize"`
	MaxSize            uint32        `json:"maxSize"`
	TableSizeBefore    uint32        `json:"tableSizeBefore"`
	TableSizeAfter     uint32        `json:"tableSizeAfter"`
	TableBefore        []HeaderField `json:"tableBefore,omitempty"`
	Operations         []TableOp     `json:"operations,omitempty"`
	Evicted            []HeaderField `json:"evicted,omitempty"`
	Fields             []HeaderField `json:"fields,omitempty"`
}

// Replay applies the commit's table operations to t.
func (c TableCommit) Replay(t *DynamicTable) error {
	if t == nil {
		return fmt.Errorf("netsec: nil replay table")
	}
	if t.maxSize != c.InitialMaxSize {
		return fmt.Errorf("netsec: replay table max size %d, want initial %d", t.maxSize, c.InitialMaxSize)
	}
	if c.AllowedMaxSize != 0 && t.allowedMaxSize != c.AllowedMaxSize {
		if c.AllowedMaxSize < t.maxSize {
			return fmt.Errorf("netsec: replay allowed max size %d is below initial max %d", c.AllowedMaxSize, t.maxSize)
		}
		t.allowedMaxSize = c.AllowedMaxSize
	}
	for i, op := range c.Operations {
		switch op.Kind {
		case TableOpSetMaxSize:
			if err := t.SetMaxSize(op.MaxSize); err != nil {
				return fmt.Errorf("netsec: replay op %d: %w", i, err)
			}
		case TableOpInsert:
			t.Add(op.Field)
		default:
			return fmt.Errorf("netsec: replay op %d: unknown kind %d", i, op.Kind)
		}
	}
	if t.Size() != c.TableSizeAfter {
		return fmt.Errorf("netsec: replay table size %d, want %d", t.Size(), c.TableSizeAfter)
	}
	if t.MaxSize() != c.MaxSize {
		return fmt.Errorf("netsec: replay table max size %d, want %d", t.MaxSize(), c.MaxSize)
	}
	return nil
}

// ReplayFromScratch reconstructs a standalone DynamicTable from the recorded
// pre-block entries and this commit's operations.
func (c TableCommit) ReplayFromScratch() (*DynamicTable, error) {
	t := NewDynamicTable(c.InitialMaxSize)
	if c.AllowedMaxSize >= t.maxSize {
		t.allowedMaxSize = c.AllowedMaxSize
	}
	t.entries = append([]HeaderField(nil), c.TableBefore...)
	t.size = 0
	for _, f := range t.entries {
		t.size += f.Size()
	}
	if t.size != c.TableSizeBefore {
		return nil, fmt.Errorf("netsec: replay snapshot size %d, want %d", t.size, c.TableSizeBefore)
	}
	if err := c.Replay(t); err != nil {
		return nil, err
	}
	return t, nil
}

// ReplayForContext replays the commit only when it belongs to ctx, preventing
// an audit record from one peer/tenant generation being applied to another.
func (c TableCommit) ReplayForContext(ctx SecurityContext, t *DynamicTable) error {
	if c.PeerID != ctx.PeerConnectionID || c.TenantID != ctx.TenantID ||
		c.SettingsGeneration != ctx.SettingsGeneration {
		return fmt.Errorf("%w: commit belongs to (%q,%q,%d), context is (%q,%q,%d)",
			ErrTenantIsolation, c.PeerID, c.TenantID, c.SettingsGeneration,
			ctx.PeerConnectionID, ctx.TenantID, ctx.SettingsGeneration)
	}
	return c.Replay(t)
}

func digestBlock(fragments [][]byte) [32]byte {
	h := sha256.New()
	for _, f := range fragments {
		_, _ = h.Write(f)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
