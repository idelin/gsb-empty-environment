// Package netsec provides checked unsafe pointer conversions for instrumented
// Go programs.
//
// A Checker records allocation bounds and element layouts, validates the
// complete target range of unsafe.Add and slice reinterpretations, checks type
// alignment, and tracks pointers temporarily stored in uintptr values across
// registered garbage-collection safe points.
package netsec
