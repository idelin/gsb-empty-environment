// Package netsec implements the SNMPv3 User-based Security Model (USM)
// timeliness and discovery checks needed at an SNMP message ingress point.
//
// The validator accepts caller-supplied local engineBoots and engineTime,
// authenticates before synchronizing message time values, enforces the 150
// second window, and returns the protocol-style notInTimeWindow and
// unknownEngineID reports. Unknown users and failed authentication both
// produce AuthenticationFailed so callers cannot accidentally build a user
// enumeration oracle.
package netsec
