package repairplan

// topologySpec describes one cluster used by the tests.
type topologySpec struct {
	rackCount      int
	rackEgress     int64
	rackIngress    int64
	osdCapacity    int64
	osdMaxConcur   int
	rackMaxConcur  int
	offline        map[string]bool // node IDs forced offline (any level)
	perRackEgress  map[string]int64
	perRackIngress map[string]int64
	perOSDCapacity map[string]int64
	perOSDEgress   map[string]int64
	perOSDIngress  map[string]int64
}

// buildTestTopology builds a Root -> Row -> Rack -> 2 Host -> 2 OSD tree.
// The default topology has four racks (16 OSDs).
func buildTestTopology(spec topologySpec) (*Topology, error) {
	if spec.rackCount == 0 {
		spec.rackCount = 4
	}
	if spec.osdCapacity == 0 {
		spec.osdCapacity = Unlimited
	}
	if spec.rackEgress == 0 {
		spec.rackEgress = Unlimited
	}
	if spec.rackIngress == 0 {
		spec.rackIngress = Unlimited
	}
	nodes := []Node{{ID: "root", Level: LevelRoot, Online: true,
		RemainingCapacity: Unlimited, IngressLimit: Unlimited, EgressLimit: Unlimited}}
	for r := 1; r <= spec.rackCount; r++ {
		rackID := rackName(r)
		rowID := "row1"
		if r == 1 {
			nodes = append(nodes, Node{ID: rowID, Level: LevelRow, ParentID: "root", Online: true,
				RemainingCapacity: Unlimited, IngressLimit: Unlimited, EgressLimit: Unlimited})
		}
		egress := spec.rackEgress
		if v, ok := spec.perRackEgress[rackID]; ok {
			egress = v
		}
		ingress := spec.rackIngress
		if v, ok := spec.perRackIngress[rackID]; ok {
			ingress = v
		}
		nodes = append(nodes, Node{
			ID: rackID, Level: LevelRack, ParentID: rowID,
			Online:            !spec.offline[rackID],
			RemainingCapacity: Unlimited,
			IngressLimit:      ingress,
			EgressLimit:       egress,
			MaxConcurrent:     spec.rackMaxConcur,
		})
		for h := 1; h <= 2; h++ {
			hostID := hostName(r, h)
			nodes = append(nodes, Node{
				ID: hostID, Level: LevelHost, ParentID: rackID,
				Online:            !spec.offline[hostID],
				RemainingCapacity: Unlimited,
				IngressLimit:      Unlimited, EgressLimit: Unlimited,
			})
			for o := 1; o <= 2; o++ {
				osdID := osdName(r, h, o)
				capacity := spec.osdCapacity
				if v, ok := spec.perOSDCapacity[osdID]; ok {
					capacity = v
				}
				osdEgress := int64(Unlimited)
				if v, ok := spec.perOSDEgress[osdID]; ok {
					osdEgress = v
				}
				osdIngress := int64(Unlimited)
				if v, ok := spec.perOSDIngress[osdID]; ok {
					osdIngress = v
				}
				nodes = append(nodes, Node{
					ID: osdID, Level: LevelOSD, ParentID: hostID,
					Online:            !spec.offline[osdID],
					RemainingCapacity: capacity,
					IngressLimit:      osdIngress,
					EgressLimit:       osdEgress,
					MaxConcurrent:     spec.osdMaxConcur,
				})
			}
		}
	}
	return NewTopology(nodes)
}

func rackName(r int) string { return "rack" + string(rune('0'+r)) }
func hostName(r, h int) string {
	return rackName(r) + "_host" + string(rune('0'+h))
}
func osdName(r, h, o int) string {
	return hostName(r, h) + "_osd" + string(rune('0'+o))
}
