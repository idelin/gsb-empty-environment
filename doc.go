// Package netsec simulates kernel file-lock semantics for a secure
// storage test host. It distinguishes POSIX process record locks from
// open file description (OFD) locks, models descriptor duplication,
// fork and close, splits and merges overlapping lock ranges, and wakes
// blocked requests in arrival order.
package netsec
