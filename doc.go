// Package netsec validates regular-expression backreferences before a policy
// host hands a pattern to a backtracking matcher.
//
// A backreference is rejected when its target group is still open at the
// reference site. That covers a group referring to itself and a group
// referring to an unfinished outer group. A reference to a group that has
// already closed is allowed, even when the reference itself is inside another
// group that is still open.
package netsec
