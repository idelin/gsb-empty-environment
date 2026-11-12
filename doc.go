// Package netsec provides a fail-closed admission policy for BACnet/IP
// gateway write traffic.
//
// The policy accepts only explicitly registered object/property pairs at
// priorities 3 through 16 (levels 1 and 2 are reserved for life safety),
// denies control services by default, and rejects an entire
// WritePropertyMultiple request if any contained write violates policy.
package netsec
