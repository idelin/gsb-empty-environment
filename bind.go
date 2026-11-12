package taskpkg

import (
	"fmt"
	"strings"
)

// HeaderNode is one ancestor in a column's ordered header path.
type HeaderNode struct {
	// Text is the original header cell text. Empty header cells are omitted
	// from the path rather than replaced by a value from another column.
	Text string
	// Level is the zero-based expanded-grid row containing this header cell.
	Level int
	// Source identifies the original header cell, including its span.
	Source CellRef
	// Role is set when the node was explicitly configured as a year or unit
	// carrier.
	Role HeaderRole
}

// ColumnBinding is the semantic binding for one data column. There is exactly
// one binding per selected data column; equal Path values are never merged.
type ColumnBinding struct {
	Column   int
	Path     []HeaderNode
	PathText string

	Year     string
	YearNode *HeaderNode
	Unit     string
	UnitNode *HeaderNode

	// HeaderSources lists the original header cells in the same order as
	// Path. Spanned slots therefore appear once even when they cover several
	// expanded columns.
	HeaderSources []CellRef

	// SourceKey identifies the original header cells contributing to Path.
	// Identity additionally includes the expanded column index, making even
	// identical logical paths distinguishable.
	SourceKey string
	Identity  string
}

// RowKind is the semantic type assigned to a body row.
type RowKind string

const (
	RowKindHeader   RowKind = "header"
	RowKindData     RowKind = "data"
	RowKindSubtotal RowKind = "subtotal"
	RowKindTotal    RowKind = "total"
	RowKindIgnored  RowKind = "ignored"
)

// RowContext is passed to a custom row rule.
type RowContext struct {
	Index    int
	Cells    []Cell
	Label    string
	IsHeader bool
}

// RowRule assigns Kind to the first row matching its criteria. Criteria are
// combined with AND; slices within one criterion are alternatives (OR).
// A rule with no criteria never matches.
type RowRule struct {
	Kind RowKind

	// LabelContains matches against the row label, case-insensitively.
	LabelContains []string
	// AnyCellContains matches when any cell text contains a value.
	AnyCellContains []string
	// Predicate is an optional custom condition.
	Predicate func(RowContext) bool
}

func (rule RowRule) matches(context RowContext) bool {
	if rule.Kind == "" {
		return false
	}

	hasCriterion := false
	if len(rule.LabelContains) > 0 {
		hasCriterion = true
		if !containsAny(context.Label, rule.LabelContains) {
			return false
		}
	}
	if len(rule.AnyCellContains) > 0 {
		hasCriterion = true
		found := false
		for _, cell := range context.Cells {
			if containsAny(cell.Text, rule.AnyCellContains) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if rule.Predicate != nil {
		hasCriterion = true
		if !rule.Predicate(context) {
			return false
		}
	}

	return hasCriterion
}

func containsAny(text string, needles []string) bool {
	text = strings.ToLower(text)
	for _, needle := range needles {
		if needle != "" && strings.Contains(text, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

// BindConfig controls data-column selection, header semantics and body-row
// typing.
type BindConfig struct {
	// HeaderSemantics explicitly marks header source cells as year or unit
	// carriers. Header text is never interpreted semantically without a
	// matching entry here.
	HeaderSemantics []HeaderSemantic

	// HeaderRowCount forces the number of leading header rows. When zero,
	// the binder uses the leading rows that contain column-header cells.
	HeaderRowCount int

	// DataColumns restricts binding to these expanded columns. When empty,
	// the binder selects all header or data columns and excludes row-label
	// columns.
	DataColumns []int

	// RowRules are tried in order for body rows. The first match wins.
	RowRules []RowRule

	// DefaultRowKind is used when no rule matches. Empty means RowKindData.
	DefaultRowKind RowKind
}

// BoundCell is a data-area cell together with its column binding identity.
type BoundCell struct {
	Column         int
	ColumnIdentity string
	Text           string
	Source         CellRef
}

// RowBinding is the typed body row and its complete source provenance.
type RowBinding struct {
	Index       int
	Kind        RowKind
	Label       string
	LabelSource *CellRef

	// Cells contains one entry per selected data column, including empty
	// cells, so callers can align values by column index.
	Cells []BoundCell
	// Sources contains every non-empty source cell in the expanded row,
	// including row-header labels. Repeated span slots share a source.
	Sources []CellRef
}

// Table is the result of binding an expanded grid.
type Table struct {
	Columns    []ColumnBinding
	Rows       []RowBinding
	HeaderRows int
}

// PathString joins path labels with the package's display separator.
func PathString(path []HeaderNode) string {
	labels := make([]string, 0, len(path))
	for _, node := range path {
		labels = append(labels, node.Text)
	}
	return strings.Join(labels, " / ")
}

// QualifiedPath includes the original source of every path node. This makes
// two equal logical paths visibly distinct.
func (binding ColumnBinding) QualifiedPath() string {
	parts := make([]string, 0, len(binding.Path))
	for _, node := range binding.Path {
		parts = append(parts, fmt.Sprintf("%s [%s]", node.Text, node.Source.Key()))
	}
	return strings.Join(parts, " / ")
}

// Bind expands header ancestry into one semantic binding per data column and
// classifies the body rows.
func Bind(grid Grid, config BindConfig) (*Table, error) {
	if err := grid.Validate(); err != nil {
		return nil, err
	}
	if config.DefaultRowKind != "" && config.DefaultRowKind != RowKindData &&
		config.DefaultRowKind != RowKindSubtotal && config.DefaultRowKind != RowKindTotal &&
		config.DefaultRowKind != RowKindIgnored && config.DefaultRowKind != RowKindHeader {
		return nil, fmt.Errorf("%w: unsupported default row kind %q",
			ErrInvalidConfig, config.DefaultRowKind)
	}

	headerRows, err := headerRowCount(grid, config.HeaderRowCount)
	if err != nil {
		return nil, err
	}
	columns, err := selectDataColumns(grid, headerRows, config.DataColumns)
	if err != nil {
		return nil, err
	}
	semantics, err := buildSemanticIndex(config.HeaderSemantics)
	if err != nil {
		return nil, err
	}

	table := &Table{
		Columns:    make([]ColumnBinding, 0, len(columns)),
		Rows:       make([]RowBinding, 0, len(grid.Rows)-headerRows),
		HeaderRows: headerRows,
	}

	for _, column := range columns {
		binding, err := bindColumn(grid, column, headerRows, semantics)
		if err != nil {
			return nil, err
		}
		table.Columns = append(table.Columns, binding)
	}

	for rowIndex := headerRows; rowIndex < len(grid.Rows); rowIndex++ {
		table.Rows = append(table.Rows, bindRow(grid.Rows[rowIndex], rowIndex, table.Columns, config))
	}

	return table, nil
}

func bindColumn(grid Grid, column, headerRows int, semantics semanticIndex) (ColumnBinding, error) {
	binding := ColumnBinding{Column: column}
	seenSources := make(map[string]bool, headerRows)

	for rowIndex := 0; rowIndex < headerRows; rowIndex++ {
		cell := grid.Rows[rowIndex][column]
		if !cell.IsColumnHeader || isBlank(cell.Text) {
			// A completely missing header level contributes no node. In
			// particular, values from neighbouring columns are never used.
			continue
		}
		key := cell.Source.Key()
		if seenSources[key] {
			// The same original cell appears repeatedly because rowspan or
			// colspan was expanded. It is one ancestor, not several.
			continue
		}
		seenSources[key] = true
		binding.Path = append(binding.Path, HeaderNode{
			Text:   cell.Text,
			Level:  rowIndex,
			Source: cell.Source,
		})
	}

	binding.PathText = PathString(binding.Path)
	sourceKeys := make([]string, 0, len(binding.Path))
	yearIndex := -1
	unitIndex := -1
	for i := range binding.Path {
		node := &binding.Path[i]
		sourceKeys = append(sourceKeys, node.Source.Key())
		binding.HeaderSources = append(binding.HeaderSources, node.Source)
		if _, ok := semantics.lookup(node.Source, HeaderRoleYear); ok {
			node.Role = HeaderRoleYear
			yearIndex = i
		}
		if _, ok := semantics.lookup(node.Source, HeaderRoleUnit); ok {
			node.Role = HeaderRoleUnit
			unitIndex = i
		}
	}

	// A column may have several configured ancestors for the same role (for
	// example a generic "年度" level and a specific "2024年" child). The
	// deepest node is the most specific one and alone determines the value.
	if yearIndex >= 0 {
		node := &binding.Path[yearIndex]
		semantic, _ := semantics.lookup(node.Source, HeaderRoleYear)
		value, err := semanticValue(semantic, *node)
		if err != nil {
			return ColumnBinding{}, fmt.Errorf("column %d: %w", column, err)
		}
		binding.Year = value
		binding.YearNode = node
	}
	if unitIndex >= 0 {
		node := &binding.Path[unitIndex]
		semantic, _ := semantics.lookup(node.Source, HeaderRoleUnit)
		value, err := semanticValue(semantic, *node)
		if err != nil {
			return ColumnBinding{}, fmt.Errorf("column %d: %w", column, err)
		}
		binding.Unit = value
		binding.UnitNode = node
	}

	binding.SourceKey = strings.Join(sourceKeys, "|")
	binding.Identity = fmt.Sprintf("col:%d@%s", column, binding.SourceKey)
	return binding, nil
}

func rowLabel(row []Cell) (string, *CellRef) {
	labels := make([]string, 0)
	var firstSource *CellRef
	for _, cell := range row {
		if !cell.IsRowHeader || isBlank(cell.Text) {
			continue
		}
		labels = append(labels, strings.TrimSpace(cell.Text))
		if firstSource == nil {
			source := cell.Source
			firstSource = &source
		}
	}
	if len(labels) == 0 {
		// Some exporters do not mark row labels. The leading non-header cell
		// is still useful to rule predicates; it is not turned into a
		// synthetic header column.
		for _, cell := range row {
			if !cell.IsColumnHeader && !isBlank(cell.Text) {
				source := cell.Source
				return strings.TrimSpace(cell.Text), &source
			}
		}
	}
	return strings.Join(labels, " | "), firstSource
}

func bindRow(row []Cell, index int, columns []ColumnBinding, config BindConfig) RowBinding {
	label, labelSource := rowLabel(row)
	context := RowContext{
		Index:    index,
		Cells:    row,
		Label:    label,
		IsHeader: rowHasColumnHeader(row),
	}

	kind := RowKindData
	if context.IsHeader {
		kind = RowKindHeader
	} else {
		if config.DefaultRowKind != "" {
			kind = config.DefaultRowKind
		}
		for _, rule := range config.RowRules {
			if rule.matches(context) {
				kind = rule.Kind
				break
			}
		}
	}

	binding := RowBinding{
		Index:       index,
		Kind:        kind,
		Label:       label,
		LabelSource: labelSource,
		Cells:       make([]BoundCell, 0, len(columns)),
	}

	seenSources := make(map[string]bool, len(row))
	for _, cell := range row {
		if isBlank(cell.Text) {
			continue
		}
		key := cell.Source.Key()
		if seenSources[key] {
			continue
		}
		seenSources[key] = true
		binding.Sources = append(binding.Sources, cell.Source)
	}

	for _, column := range columns {
		cell := row[column.Column]
		binding.Cells = append(binding.Cells, BoundCell{
			Column:         column.Column,
			ColumnIdentity: column.Identity,
			Text:           cell.Text,
			Source:         cell.Source,
		})
	}
	return binding
}
