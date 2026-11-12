// Package netsec provides scan-result selection for OWE transition mode.
//
// A transition is accepted only when the open BSS and OWE BSS reference each
// other and both BSSID and SSID claims agree. Failed, ambiguous, one-way, or
// rejected OWE transitions never silently downgrade to an open connection.
package netsec
