// Package netsec compares two locked dependency graphs and reports install-time
// capabilities that can become newly reachable after an update.
//
// Dependencies are rooted directed graphs. Edges may be guarded by boolean
// conditions over at most 16 named platform flags. Nodes list the install-time
// capabilities they provide. Compare exhaustively enumerates platform
// configurations, evaluates both graph versions under the same configuration,
// and returns each capability that is newly active in the new graph together
// with a shortest root-to-provider dependency path and a witness configuration.
//
// The zero value of Condition is true, so an unconditional Edge needs no
// explicit condition:
//
//	graph := netsec.Graph{
//		Root: "app",
//		Nodes: []netsec.Node{
//			{ID: "app"},
//			{ID: "tool", Capabilities: []string{"postinstall"}},
//		},
//		Edges: []netsec.Edge{
//			{From: "app", To: "tool", When: netsec.Flag("linux")},
//		},
//	}
package netsec
