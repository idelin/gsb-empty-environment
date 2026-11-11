// Package netsec models protocol endpoints communicating over bounded FIFO
// channels whose overflow policy is either drop-new or drop-old. A
// verification host supplies endpoint state machines, channel capacities and
// success-state bindings; Check exhaustively explores the reachable state
// space and reports any trace that enters a success state without receiving
// the bound messages. Budget exhaustion yields Unknown rather than an
// unsound Safe verdict.
package netsec
