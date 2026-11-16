// Package netsec implements admission control for a BACnet/IP gateway's
// forwarding loop.
//
// A Policy is built once from an explicit list of writable object/property
// pairs. Only WriteProperty and WritePropertyMultiple requests can be admitted;
// device-control services are denied. Writes must carry a priority in the
// configured interval, which never includes the reserved priorities 1 and 2.
// WritePropertyMultiple is all-or-nothing: if any entry targets an unregistered
// object or property, uses a forbidden service or priority, or is malformed,
// the entire request is rejected.
package netsec
