package mappingadmission_test

import (
	"fmt"

	"example.org/netsec/q0648/mappingadmission"
)

// This is the shape of the call an embedded UPnP SOAP action handler makes
// once it has parsed the AddPortMapping action arguments.
func ExampleTable_Admit() {
	table := mappingadmission.NewTable()
	cfg := mappingadmission.Config{
		MaxLeaseSeconds:       86400,
		ReservedExternalPorts: map[uint16]struct{}{8080: {}},
		GatewayHosts:          []string{"192.0.2.1"},
	}

	req := mappingadmission.Request{
		RemoteHost:     "",
		ExternalPort:   5000,
		Protocol:       "TCP",
		InternalPort:   8080,
		InternalClient: "192.0.2.10",
		LeaseDuration:  3600,
		HostHeader:     "192.0.2.1:49152",
	}

	// conn.RemoteAddr().String() from the inbound SOAP control connection.
	sourceAddr := "192.0.2.10:54321"

	mapping, err := table.Admit(req, sourceAddr, cfg)
	if err != nil {
		// Map the sentinel error onto the matching UPnP SOAP fault code.
		fmt.Println("rejected:", err)
		return
	}
	if err := table.Install(*mapping); err != nil {
		fmt.Println("install failed:", err)
		return
	}

	fmt.Printf("installed %d/%s -> %s:%d for %ds\n",
		mapping.ExternalPort, mapping.Protocol,
		mapping.InternalClient, mapping.InternalPort, mapping.LeaseDuration)
	// Output: installed 5000/TCP -> 192.0.2.10:8080 for 3600s
}

func ExampleTable_Admit_dnsRebinding() {
	table := mappingadmission.NewTable()
	cfg := mappingadmission.Config{
		MaxLeaseSeconds: 86400,
		GatewayHosts:    []string{"192.0.2.1"},
	}

	req := mappingadmission.Request{
		ExternalPort:   5000,
		Protocol:       "TCP",
		InternalPort:   8080,
		InternalClient: "192.0.2.10",
		LeaseDuration:  3600,
		HostHeader:     "attacker.example",
	}

	if _, err := table.Admit(req, "192.0.2.10:54321", cfg); err != nil {
		fmt.Println("rejected:", err)
	}
	// Output: rejected: mappingadmission: Host header is not a gateway address
}
