// Package netsec creates upload directories without following symlinks.
//
// Paths are walked one component at a time relative to a caller-provided
// tenant root descriptor. Every component is opened with O_NOFOLLOW and
// checked with fstat before it is used as the next parent directory.
package netsec
