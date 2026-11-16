// Package netsec decides whether a Git repository may be trusted before its
// local configuration is read.
//
// Local safe.directory values are never an authorization source. Only
// safe.directory values from Git's system and global configuration scopes are
// considered. Repository discovery does not cross a directory owned by another
// user unless that path is allowed by those protected configuration scopes.
package netsec
