// Package netsec provides group-read authorization for NNTP command
// handlers.
//
// Callers construct an Authorizer from the groups or wildmat patterns a
// client may read and use it at the points where a command would otherwise
// cross a group boundary:
//
//   - LookupArticle gates ARTICLE, HEAD and BODY by message ID. An article is
//     visible only if its Xref header contains a readable group. Missing and
//     unreadable articles both return ErrNoSuchArticle (the canonical 430
//     reply) with identical text and length.
//   - FilterHDR and FilterXPAT suppress results from unreadable groups.
//   - FilterNEWNEWS expands a wildmat only over readable groups, then
//     retains results that originated in those groups.
//   - FilterArticle and FilterXref remove unreadable group locations from an
//     Xref header before an article is returned to the caller.
package netsec
