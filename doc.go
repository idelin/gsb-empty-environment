// Package netsec provides lexical admission checks for paths supplied by
// untrusted users.
//
// The checks in this package never call the operating system or touch the
// filesystem. This matters on Windows: opening or statting a UNC path can
// make the SMB client send the service account's NTLM credentials to a
// remote host before the caller has had a chance to inspect the path.
//
// Callers should validate a path with Policy.Check before passing it to
// os.Stat, os.Open, filepath.EvalSymlinks, or any other filesystem API.
// Symlink and reparse-point resolution is deliberately delegated to a
// callback because performing that resolution inside this package would
// itself require a filesystem call.
package netsec
