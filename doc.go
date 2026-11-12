// Package netsec provides layer-2 security guards for PROFINET controllers.
//
// The DCP guard (Guard) is fed the raw Ethernet frames delivered by the
// controller's packet capture path (AF_PACKET on Linux, BPF on macOS):
//
//	guard, _ := netsec.New(netsec.Config{
//	    Stations: map[string]netsec.DeviceIdentity{
//	        "io-device-1": {VendorID: 0x002A, DeviceID: 0x1234},
//	    },
//	    EngineeringStationMACs: []string{"02:00:00:00:00:01"},
//	})
//
//	func onFrame(frame []byte) {
//	    decision := guard.ProcessFrame(frame)
//	    if decision.Action == netsec.ActionDrop {
//	        report(decision.Event) // surface EventDuplicateAnswer, etc.
//	    }
//	    if guard.StationConflict("io-device-1") {
//	        // Two distinct MACs claimed the same name: do not connect.
//	    }
//	}
//
// It enforces that DCP Set requests (EtherType 0x8892, FrameID 0xFEFD) only
// come from registered engineering stations, that Identify answers carry the
// configured VendorID/DeviceID and never share one station name across two
// MACs, and that frames with out-of-bounds block lengths or repeated options
// are dropped.
package netsec
