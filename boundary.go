package netsec

import "fmt"

// Layer is one node on the organization path from the tree root down to the
// account that owns the resource.
//
// An EffectAllow layer is an allow boundary: access is confined to Resources,
// and the running authorization is intersected with it. An empty Resources set
// on an allow layer therefore denies everything underneath the node.
//
// An EffectDeny layer is an explicit refusal: Resources are subtracted from
// the running authorization regardless of what descendant layers allow.
type Layer struct {
	NodeID    string      `json:"node_id"`
	Effect    Effect      `json:"effect"`
	Resources IntervalSet `json:"resources"`
}

func validateLayers(layers []Layer) error {
	seen := map[string]struct{}{}
	for depth, layer := range layers {
		if layer.NodeID == "" {
			return fmt.Errorf("netsec: layer at depth %d has an empty node id", depth)
		}
		if _, dup := seen[layer.NodeID]; dup {
			return fmt.Errorf("netsec: layer node %q appears more than once on the path", layer.NodeID)
		}
		seen[layer.NodeID] = struct{}{}
		switch layer.Effect {
		case EffectAllow, EffectDeny:
		default:
			return fmt.Errorf("netsec: layer %q has invalid effect %q", layer.NodeID, layer.Effect)
		}
	}
	return nil
}
