// Package netsec implements the select-before-execute control-command state
// for an IEC 60870-5-104 slave.
//
// It enforces the pairing rule for single commands (C_SC_NA_1, type 45): a
// select (S/E=1) arms one information object for one connection and must be
// confirmed within 30 seconds by an execute (S/E=0) from the same connection,
// on the same IOA, with the same command value. Direct executes on points
// configured select-first are rejected, a cancellation (COT 8) clears the
// pending selection, and selections that exceed the window are voided.
package netsec
