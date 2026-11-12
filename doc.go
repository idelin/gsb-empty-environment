// Package taskpkg decodes DNS names once for shared security-check and
// business callbacks. Compression pointers are two bytes, strictly backwards,
// and valid only when they target a previously validated complete label
// start. Expanded names are limited to 255 wire bytes.
package taskpkg
