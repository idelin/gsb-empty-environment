// Package pathsplit safely splits an HTTP request URL path into a PHP
// SCRIPT_FILENAME (and SCRIPT_NAME) plus PATH_INFO for a Go FastCGI gateway.
//
// The split is evaluated against a virtual, in-memory view of the site root
// (an implementation of FS), so the package never touches the real disk and is
// fully deterministic and testable. A path segment is only treated as a script
// when every one of the following holds:
//
//   - its normalized name ends in ".php" (case-insensitive), with trailing
//     dots and spaces stripped after percent-decoding;
//   - the corresponding entry really exists as a file in the virtual listing;
//   - its virtual location is inside a configured script directory;
//   - every intermediate and trailing symlink is resolved, symlink loops are
//     rejected, and the resolved path stays inside the site root;
//   - neither the virtual nor the resolved path falls inside an upload
//     directory.
//
// When several ".php" segments are candidates, the foremost one that satisfies
// all rules wins and the remainder becomes PATH_INFO.
package pathsplit
