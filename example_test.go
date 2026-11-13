package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0678"
)

// ExampleValidate walks through the headline hijacking scenarios from the
// admission policy requirements.
func ExampleValidate() {
	opts := netsec.Options{
		AllowedCIDRs: []string{"203.0.113.0/24"},
		Reserved: netsec.ReservedSet{
			CIDRs: []string{"10.96.0.0/12", "10.244.0.0/16"},
			IPs:   []string{"10.0.0.5", "10.96.0.10"}, // node IP, DNS Service IP
		},
	}

	attempts := []*netsec.Service{
		{
			Namespace: "tenant-a",
			Spec:      netsec.ServiceSpec{ExternalIPs: []string{"10.0.0.5"}},
		},
		{
			Namespace: "tenant-a",
			Spec:      netsec.ServiceSpec{ExternalIPs: []string{"::ffff:10.96.0.10"}},
		},
		{
			Namespace: "tenant-a",
			Spec:      netsec.ServiceSpec{ExternalIPs: []string{"010.0.0.1"}},
		},
		{
			Namespace: "tenant-a",
			Spec: netsec.ServiceSpec{
				Type:         netsec.ServiceTypeExternalName,
				ExternalName: "db.tenant-b.svc.cluster.local",
			},
		},
		{
			Namespace: "tenant-a",
			Spec:      netsec.ServiceSpec{ExternalIPs: []string{"203.0.113.42"}},
		},
	}

	for _, svc := range attempts {
		if err := netsec.Validate(svc, opts); err != nil {
			fmt.Printf("DENY: %v\n", err)
		} else {
			fmt.Println("ALLOW")
		}
	}

	// Output:
	// DENY: spec.externalIPs[0]: 10.0.0.5 is a reserved cluster address
	// spec.externalIPs[0]: 10.0.0.5 is not within any address range allowed for this namespace
	// DENY: spec.externalIPs[0]: 10.96.0.10 is a reserved cluster address
	// spec.externalIPs[0]: 10.96.0.10 falls within reserved range 10.96.0.0/12
	// spec.externalIPs[0]: 10.96.0.10 is not within any address range allowed for this namespace
	// DENY: spec.externalIPs[0]: "010.0.0.1" is not a valid IPv4 or IPv6 address (octal, hexadecimal and leading-zero IPv4 notation are rejected)
	// DENY: spec.externalName: db.tenant-b.svc.cluster.local points to Service "db" in namespace "tenant-b", which is not the current namespace "tenant-a"
	// ALLOW
}
