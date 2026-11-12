// Package netsec implements MLS-style epoch advancement for a group message
// service. Commit processing validates the parent epoch and group context,
// aggregates only active leaf contributions into the commit secret, and
// derives the next epoch secret with HKDF-SHA256.
package netsec
