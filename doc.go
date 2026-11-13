// Package netsec implements authorization matching for sudoers-style
// privilege-elevation rules.
//
// Programs are matched only by literal absolute path. Argument patterns are
// matched with fnmatch semantics where '*' also matches slashes and spaces.
// Policy rules additionally reject parent-directory path segments and options
// that are not spelled out literally by the matched pattern.
package netsec
