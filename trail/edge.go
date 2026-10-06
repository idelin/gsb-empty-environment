// Package trail rebuilds logistics trajectories from directed edges.
//
// A trajectory is an Eulerian trail: starting from a given node it uses
// every edge exactly once. Among all valid trajectories the library picks
// the one whose edge-id sequence is lexicographically smallest.
package trail

// Edge is a single directed leg of a logistics trajectory. IDs are unique
// across the whole edge set; parallel edges between the same pair of nodes
// are distinguished by their IDs.
type Edge struct {
	ID   string
	From string
	To   string
}
