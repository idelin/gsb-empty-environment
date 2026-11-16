package netsec_test

import (
	"fmt"
	"time"

	netsec "example.org/netsec/q0820"
)

func ExampleEvaluate() {
	const config = `<network-security-config>
  <base-config cleartextTrafficPermitted="false">
    <pin-set expiration="2030-01-01">
      <pin digest="SHA-256">base-pin</pin>
    </pin-set>
  </base-config>
  <domain-config cleartextTrafficPermitted="true">
    <domain includeSubdomains="true">example.com</domain>
    <trust-anchors>
      <certificates src="system"/>
      <certificates src="user"/>
    </trust-anchors>
  </domain-config>
</network-security-config>`

	policy, err := netsec.Evaluate([]byte(config), "api.example.com", netsec.Options{
		TargetSDK: 35,
		Now:       time.Date(2026, time.November, 16, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(policy.CleartextTrafficPermitted)
	fmt.Println(policy.TrustAnchors.System, policy.TrustAnchors.User)
	fmt.Println(policy.Pinning, len(policy.Pins))
	// Output:
	// true
	// true true
	// true 1
}
