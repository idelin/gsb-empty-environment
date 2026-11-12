// Package netsec implements kernel-side path resolution for a gofer-style
// sandboxed filesystem. The userspace kernel resolves walk and open
// requests component by component against the mount root, expands
// symbolic links itself instead of delegating them to the host, and
// clamps ".." at the mount root so container-controlled names and links
// can never address host files outside the mount.
package netsec
