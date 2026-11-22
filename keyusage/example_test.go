package keyusage_test

import (
	"errors"
	"fmt"

	"prescreen.local/ps-a4f2eba884/keyusage"
)

func ExampleEnforcer() {
	sink := keyusage.NewMemorySink()
	enf := keyusage.NewEnforcer(sink)

	signingKey := keyusage.Key{
		ID:        "sig-1",
		Algorithm: keyusage.AlgECDSAP256,
		Usages:    []keyusage.Usage{keyusage.UsageSign, keyusage.UsageVerify},
	}
	if err := enf.Register(signingKey); err != nil {
		panic(err)
	}

	// 合法用途放行。
	fmt.Println("sign allowed:", enf.Check(signingKey, keyusage.UsageSign) == nil)

	// 签名密钥用于加密：跨用途族拒绝并审计。
	err := enf.Check(signingKey, keyusage.UsageEncrypt)
	var denied *keyusage.DenialError
	if errors.As(err, &denied) {
		fmt.Println("encrypt denied:", denied.Reason)
	}

	// 弱算法同样被拦截。
	weak := keyusage.Key{ID: "legacy", Algorithm: keyusage.Alg3DESCBC,
		Usages: []keyusage.Usage{keyusage.UsageEncrypt}}
	fmt.Println("weak denied:", errors.As(enf.Check(weak, keyusage.UsageEncrypt), &denied))

	fmt.Println("audit events:", len(sink.Events()))
	// Output:
	// sign allowed: true
	// encrypt denied: cross_family_usage
	// weak denied: true
	// audit events: 2
}

func ExampleEnforcer_derive() {
	enf := keyusage.NewEnforcer(keyusage.NewMemorySink())

	root := keyusage.Key{
		ID:        "root",
		Algorithm: keyusage.AlgHKDFSHA256,
		Usages: []keyusage.Usage{
			keyusage.UsageDerive,
			keyusage.UsageEncrypt,
			keyusage.UsageDecrypt,
		},
	}
	if err := enf.Register(root); err != nil {
		panic(err)
	}

	// 子密钥只继承父用途子集。
	child, err := enf.Derive("root", "child", keyusage.AlgAESGCM256,
		[]keyusage.Usage{keyusage.UsageEncrypt})
	fmt.Println("derived:", child.ID, "err:", err)

	// 试图获得父密钥未授权的签名用途：拒绝。
	_, err = enf.Derive("root", "escalated", keyusage.AlgECDSAP256,
		[]keyusage.Usage{keyusage.UsageSign})
	var denied *keyusage.DenialError
	if errors.As(err, &denied) {
		fmt.Println("escalation denied:", denied.Reason)
	}

	// Output:
	// derived: child err: <nil>
	// escalation denied: derived_usage_exceeds_parent
}

func ExampleEnforcer_wrapKey() {
	enf := keyusage.NewEnforcer(keyusage.NewMemorySink())

	wrapKey := keyusage.Key{
		ID:        "wrap-1",
		Algorithm: keyusage.AlgAESKeyWrap256,
		Usages:    []keyusage.Usage{keyusage.UsageWrapKey, keyusage.UsageUnwrapKey},
	}
	if err := enf.Register(wrapKey); err != nil {
		panic(err)
	}

	fmt.Println("wrap allowed:", enf.Check(wrapKey, keyusage.UsageWrapKey) == nil)

	err := enf.Check(wrapKey, keyusage.UsageDecrypt)
	var denied *keyusage.DenialError
	if errors.As(err, &denied) {
		fmt.Println("business decrypt denied:", denied.Reason)
	}

	// Output:
	// wrap allowed: true
	// business decrypt denied: wrap_key_for_business_data
}
