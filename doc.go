// Package netsec computes capability closures over directory object
// graphs for a launch-plan host. After a process switches its root
// directory it may still hold inherited directory handles (including
// its implicit working-directory handle) that reach back outside the
// new root. Analyze computes, for every handle, the set of directory
// objects reachable through child and parent ("..") traversal, where
// parent traversal is capped at the current root for handles inside
// the root but is not capped for handles that start outside it. It
// reports escape witnesses and the minimal set of handles that must
// be closed (or, for the working directory, repointed) so that no
// remaining handle can reach any object outside the root.
package netsec
