// Package netsec provides admission control for DNP3 application requests at
// a security gateway.
//
// Requests are first reassembled from transport fragments using FIR/FIN and
// transport sequence numbers. Only after reassembly is the application
// function code examined. Cold restart (FC 13) and hot restart (FC 14) have
// separate source-address authorizations. SELECT (FC 3) and OPERATE (FC 4)
// must share a source, destination, and application sequence number, and
// OPERATE must arrive before the SELECT lifetime expires. Unknown function
// codes are rejected.
package netsec
