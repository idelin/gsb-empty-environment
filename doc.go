// Package netsec implements a small, security-focused TFTP transfer session
// engine. Callers receive UDP datagrams themselves and hand the initial
// request to Server.Accept; subsequent datagrams are handed to the returned
// Session through Session.HandlePacket.
package netsec
