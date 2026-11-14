package netsec

import (
	"context"
	"fmt"
	"sort"
)

// Row is one dataset record. Column values are addressed by filter keys.
type Row map[string]string

// DataSource serves dataset rows. Implementations are part of the trust
// boundary: Query must only return rows of the explicitly named tenant and
// dataset, and must apply every equality filter.
type DataSource interface {
	Query(ctx context.Context, tenantID, dataset string, filters Filters) (columns []string, rows []Row, err error)
}

// MemoryDataSource is a deterministic in-process DataSource for the export
// service and its tests. Tenant isolation is enforced here as defense in
// depth even though the service already rejects cross-tenant callers.
type MemoryDataSource struct {
	// data maps tenant -> dataset -> rows.
	data map[string]map[string][]Row
}

// NewMemoryDataSource constructs an empty MemoryDataSource.
func NewMemoryDataSource() *MemoryDataSource {
	return &MemoryDataSource{data: make(map[string]map[string][]Row)}
}

// AddTable registers rows for a tenant/dataset pair.
func (d *MemoryDataSource) AddTable(tenantID, dataset string, rows []Row) {
	if d.data[tenantID] == nil {
		d.data[tenantID] = make(map[string][]Row)
	}
	d.data[tenantID][dataset] = append([]Row(nil), rows...)
}

// Query implements DataSource with strict tenant scoping.
func (d *MemoryDataSource) Query(_ context.Context, tenantID, dataset string, filters Filters) ([]string, []Row, error) {
	datasets, ok := d.data[tenantID]
	if !ok {
		return nil, nil, fmt.Errorf("%w: tenant %q", ErrCrossTenant, tenantID)
	}
	rows, ok := datasets[dataset]
	if !ok {
		return nil, nil, fmt.Errorf("netsec: dataset %q not found in tenant %q", dataset, tenantID)
	}

	columnSet := make(map[string]struct{})
	var matched []Row
	for _, row := range rows {
		if !rowMatches(row, filters) {
			continue
		}
		cp := make(Row, len(row))
		for k, v := range row {
			cp[k] = v
			columnSet[k] = struct{}{}
		}
		matched = append(matched, cp)
	}

	columns := make([]string, 0, len(columnSet))
	for col := range columnSet {
		columns = append(columns, col)
	}
	sort.Strings(columns)
	return columns, matched, nil
}

// rowMatches applies all equality predicates. A missing column never matches
// a filter on that column.
func rowMatches(row Row, filters Filters) bool {
	for key, want := range filters {
		got, ok := row[key]
		if !ok || got != want {
			return false
		}
	}
	return true
}
