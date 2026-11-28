package federate

import (
	"fmt"
	"strings"
)

// DistKind is a physical data distribution strategy.
type DistKind string

const (
	HashKind       DistKind = "hash"
	RangeKind      DistKind = "range"
	ReplicatedKind DistKind = "replicated"
)

// Distribution describes how a table is split over shards.
type Distribution struct {
	Kind       DistKind
	KeyColumns []string
	ShardCount int
	HashSeed   uint32
	Bounds     []Value
	Nodes      []string
}

// HashDist creates a hash distribution over the given key columns.
func HashDist(keys []string, shards int) Distribution {
	return Distribution{Kind: HashKind, KeyColumns: append([]string(nil), keys...), ShardCount: shards}
}

// RangeDist creates a range distribution over one key column. Bounds are the
// lower bounds of shards 1..n; shard i owns [Bounds[i-1], Bounds[i]).
func RangeDist(key string, bounds ...Value) Distribution {
	return Distribution{
		Kind:       RangeKind,
		KeyColumns: []string{key},
		ShardCount: len(bounds) + 1,
		Bounds:     append([]Value(nil), bounds...),
	}
}

// ReplicatedDist creates a fully replicated table with a copy on every shard.
func ReplicatedDist(shards int) Distribution {
	return Distribution{Kind: ReplicatedKind, ShardCount: shards}
}

func (d Distribution) validate(schema *Schema) error {
	if d.ShardCount <= 0 {
		return fmt.Errorf("shard count must be positive")
	}
	switch d.Kind {
	case HashKind, RangeKind:
		if len(d.KeyColumns) == 0 {
			return fmt.Errorf("%s distribution requires at least one key column", d.Kind)
		}
		for _, k := range d.KeyColumns {
			if _, ok := schema.Column(k); !ok {
				return fmt.Errorf("distribution key %q not found in schema", k)
			}
		}
		if d.Kind == RangeKind {
			if len(d.KeyColumns) != 1 {
				return fmt.Errorf("range distribution currently supports exactly one key column")
			}
			if len(d.Bounds) != d.ShardCount-1 {
				return fmt.Errorf("range distribution needs %d bounds, got %d", d.ShardCount-1, len(d.Bounds))
			}
			for i := 1; i < len(d.Bounds); i++ {
				c, ok := Compare(d.Bounds[i-1], d.Bounds[i])
				if !ok || c >= 0 {
					return fmt.Errorf("range bounds must be strictly ascending")
				}
			}
		}
	case ReplicatedKind:
		if len(d.KeyColumns) != 0 {
			return fmt.Errorf("replicated distribution must not declare key columns")
		}
	default:
		return fmt.Errorf("unknown distribution kind %q", d.Kind)
	}
	if len(d.Nodes) > 0 && len(d.Nodes) != d.ShardCount {
		return fmt.Errorf("nodes length %d does not match shard count %d", len(d.Nodes), d.ShardCount)
	}
	return nil
}

// Signature returns a canonical layout signature. Two sharded tables are
// co-located only when these signatures are equal.
func (d Distribution) Signature() string {
	var b strings.Builder
	b.WriteString(string(d.Kind))
	b.WriteString("|shards=")
	b.WriteString(fmt.Sprint(d.ShardCount))
	b.WriteString("|keys=")
	b.WriteString(strings.Join(d.KeyColumns, ","))
	if d.Kind == HashKind {
		b.WriteString("|seed=")
		b.WriteString(fmt.Sprint(d.HashSeed))
	}
	if d.Kind == RangeKind {
		b.WriteString("|bounds=")
		for _, v := range d.Bounds {
			b.WriteString(v.String())
			b.WriteString(";")
		}
	}
	return b.String()
}

// CompatibleLayout reports whether two distributions define the same mapping
// from key values to shard numbers.
func CompatibleLayout(a, b Distribution) bool {
	if a.Kind == ReplicatedKind && b.Kind == ReplicatedKind {
		return true
	}
	return a.Signature() == b.Signature()
}

// Shard is one physical data partition.
type Shard struct {
	ID   int
	Node string
	Rows []Row
}

// Table is a sharded table populated with rows.
type Table struct {
	Name   string
	Schema *Schema
	Dist   Distribution
	Shards []Shard
}

// NewTable validates and distributes rows into shards.
func NewTable(name string, schema *Schema, dist Distribution, rows []Row) (*Table, error) {
	if name == "" {
		return nil, fmt.Errorf("table name must not be empty")
	}
	if err := schema.validate(); err != nil {
		return nil, fmt.Errorf("table %s: %w", name, err)
	}
	if err := dist.validate(schema); err != nil {
		return nil, fmt.Errorf("table %s: %w", name, err)
	}
	t := &Table{Name: name, Schema: schema, Dist: dist, Shards: make([]Shard, dist.ShardCount)}
	for i := 0; i < dist.ShardCount; i++ {
		node := fmt.Sprintf("node-%d", i)
		if len(dist.Nodes) > 0 {
			node = dist.Nodes[i]
		}
		t.Shards[i] = Shard{ID: i, Node: node}
	}
	for rowIdx, in := range rows {
		if len(in.Values) != len(schema.Columns) {
			return nil, fmt.Errorf("table %s row %d: expected %d values, got %d", name, rowIdx, len(schema.Columns), len(in.Values))
		}
		vals := append([]Value(nil), in.Values...)
		for i := range vals {
			if vals[i].Type == TypeString && vals[i].Collation == "" {
				vals[i].Collation = schema.Columns[i].Collation
			}
		}
		row := RowForTable(name, schema, vals...)
		if dist.Kind == ReplicatedKind {
			for i := range t.Shards {
				t.Shards[i].Rows = append(t.Shards[i].Rows, row)
			}
			continue
		}
		shard, err := dist.shardFor(schema, vals)
		if err != nil {
			return nil, fmt.Errorf("table %s row %d: %w", name, rowIdx, err)
		}
		t.Shards[shard].Rows = append(t.Shards[shard].Rows, row)
	}
	return t, nil
}

func (d Distribution) shardFor(schema *Schema, values []Value) (int, error) {
	keys := make([]Value, len(d.KeyColumns))
	for i, name := range d.KeyColumns {
		idx := schema.index(name)
		if idx < 0 {
			return 0, fmt.Errorf("distribution key %q not found", name)
		}
		keys[i] = values[idx]
	}
	switch d.Kind {
	case HashKind:
		var h uint64 = 1469598103934665603 ^ uint64(d.HashSeed)
		for _, v := range keys {
			s := HashKey(v)
			for i := 0; i < len(s); i++ {
				h ^= uint64(s[i])
				h *= 1099511628211
			}
		}
		return int(h % uint64(d.ShardCount)), nil
	case RangeKind:
		if len(keys) != 1 {
			return 0, fmt.Errorf("range distribution requires one key")
		}
		if keys[0].IsNull() {
			return 0, nil
		}
		for i, bound := range d.Bounds {
			c, ok := Compare(keys[0], bound)
			if !ok {
				return 0, fmt.Errorf("range key and bound are not comparable")
			}
			if c < 0 {
				return i, nil
			}
		}
		return len(d.Bounds), nil
	default:
		return 0, fmt.Errorf("cannot compute shard for %s distribution", d.Kind)
	}
}

// ScanShards returns the shard IDs a query must scan. A replicated table is
// read from exactly one replica to avoid duplicate rows.
func (t *Table) ScanShards() []int {
	if t.Dist.Kind == ReplicatedKind {
		return []int{0}
	}
	ids := make([]int, len(t.Shards))
	for i := range ids {
		ids[i] = i
	}
	return ids
}

// FullRows returns rows in deterministic global order (shard, row).
func (t *Table) FullRows() []Row {
	if t.Dist.Kind == ReplicatedKind {
		return append([]Row(nil), t.Shards[0].Rows...)
	}
	var out []Row
	for _, sh := range t.Shards {
		out = append(out, sh.Rows...)
	}
	return out
}

// RowCount returns the logical row count (not counting replicas).
func (t *Table) RowCount() int {
	if t.Dist.Kind == ReplicatedKind {
		return len(t.Shards[0].Rows)
	}
	n := 0
	for _, sh := range t.Shards {
		n += len(sh.Rows)
	}
	return n
}

// Catalog is a table registry.
type Catalog struct {
	tables map[string]*Table
	order  []string
}

func NewCatalog(tables ...*Table) (*Catalog, error) {
	c := &Catalog{tables: make(map[string]*Table)}
	for _, t := range tables {
		if err := c.Add(t); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *Catalog) Add(t *Table) error {
	if t == nil {
		return fmt.Errorf("nil table")
	}
	if c.tables == nil {
		c.tables = make(map[string]*Table)
	}
	key := strings.ToLower(t.Name)
	if _, exists := c.tables[key]; exists {
		return fmt.Errorf("duplicate table %q", t.Name)
	}
	c.tables[key] = t
	c.order = append(c.order, key)
	return nil
}

func (c *Catalog) Table(name string) (*Table, bool) {
	if c == nil {
		return nil, false
	}
	t, ok := c.tables[strings.ToLower(name)]
	return t, ok
}

func (c *Catalog) Tables() []*Table {
	if c == nil {
		return nil
	}
	out := make([]*Table, 0, len(c.order))
	for _, k := range c.order {
		out = append(out, c.tables[k])
	}
	return out
}
