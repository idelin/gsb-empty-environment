// Package netsec provides a streaming, redaction-aware formatting layer for
// logging hosts.
//
// # Why
//
// Truncating a large field and then masking it is unsafe: truncation can cut a
// secret into a prefix that no longer matches whole-value masking rules and
// therefore leaks into the log. This package inverts the order.
//
// # Contract
//
//   - Arguments are Arg values bound to fixed template positions; the
//     Sensitive flag travels with the value it protects.
//   - A sensitive argument is replaced wholesale by the redaction token before
//     any width, precision or output budget is evaluated. The original value
//     is never truncated, formatted or written.
//   - Non-sensitive truncation (output budgets and string precision) is
//     performed on UTF-8 code-point boundaries, so a multi-byte rune is never
//     split.
//   - A malformed template never falls back to concatenating raw arguments.
//     The output receives a fixed error marker and the call returns a
//     FormatError, matched by errors.Is(err, ErrFormat).
//
// Use Format to stream into an io.Writer, or FormatString/FormatBytes for
// in-memory callers. WithBudget bounds emitted bytes and reserves room, inside
// the same budget, for the truncation marker.
package netsec
