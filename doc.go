// Package taskpkg tracks cursor-pagination progress for a collection host.
//
// The tracker deliberately distinguishes "the upstream said there is no next
// page" from every heuristic stop condition. An empty page, a page containing
// only already-seen pinned keys, or a page whose boundary overlaps the next
// page never means the collection is complete. Completion is reported only
// when a page explicitly has no next cursor.
package taskpkg
