// Package netsec provides side-effect-free admission checks for paths supplied
// by untrusted users.
//
// CheckPath and Checker.Check only inspect the path string. They do not stat,
// open, clean, or resolve anything. This makes it safe to reject network and
// device paths before a Windows file API can initiate outbound SMB or WebDAV
// authentication.
package netsec
