// Package taskpkg binds the columns of an expanded web-table grid to the
// multi-level header cells that cover them.
//
// The input grid is already expanded: every position in Grid.Rows is one
// occupied cell slot. Cells that came from the same original cell (for
// example a colspan or rowspan header) share the same Cell.Source value.
// This package never merges columns with identical labels. A duplicate
// logical path is represented by separate ColumnBinding values whose
// SourceKey and Identity include the originating header cells.
package taskpkg
