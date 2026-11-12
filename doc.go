// Package netsec contains security-oriented building blocks for Go encryption
// hosts.
//
// The age plugin session implementation in this package speaks the host side
// of the age plugin protocol v1 over arbitrary byte streams supplied by the
// caller. It never starts processes itself: the validated plugin executable
// name is exposed through [Session.BinaryName] so that callers stay in control
// of process creation.
package netsec
