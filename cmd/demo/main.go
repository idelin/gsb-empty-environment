// Command demo prints canonical forms and keys for sample requests and
// demonstrates cache isolation across a config upgrade.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"canonkey"
)

func show(k *canonkey.Keyer, label string, r canonkey.Request) canonkey.Key {
	key, err := k.BuildKey(r)
	if err != nil {
		fmt.Printf("%-28s ERROR: %v\n", label, err)
		return canonkey.Key{}
	}
	fmt.Printf("%-28s canon=%s\n%-28s key=%s\n", label, key.Canon, "", key)
	return key
}

func main() {
	keyer := canonkey.NewKeyer(canonkey.Limits{})
	params := map[string]any{
		"user_id": json.Number("12345"),
		"flags":   []any{"beta", "alpha"},
		"limits":  map[string]any{"qps": json.Number("100"), "burst": json.Number("1.0")},
	}

	fmt.Println("== canonical forms & keys ==")
	show(keyer, "baseline", canonkey.Request{Tenant: "acme", ConfigVersion: "cfg-v1", Params: params})
	show(keyer, "number 1", canonkey.Request{Tenant: "acme", ConfigVersion: "cfg-v1",
		Params: map[string]any{"v": json.Number("1")}})
	show(keyer, "string \"1\"", canonkey.Request{Tenant: "acme", ConfigVersion: "cfg-v1",
		Params: map[string]any{"v": "1"}})
	show(keyer, "params absent", canonkey.Request{Tenant: "acme", ConfigVersion: "cfg-v1", Params: nil})
	show(keyer, "params null", canonkey.Request{Tenant: "acme", ConfigVersion: "cfg-v1", Params: canonkey.Null})
	show(keyer, "config omitted", canonkey.Request{Tenant: "acme", Params: params})

	fmt.Println()
	fmt.Println("== cache across config upgrade ==")
	store := canonkey.NewStore()
	defer store.Close()
	calls := 0
	res := &canonkey.Resolver{
		Keyer: keyer,
		Store: store,
		Source: func(ctx context.Context, r canonkey.Request) (any, error) {
			calls++
			return fmt.Sprintf("result-for-%s#%d", r.ConfigVersion, calls), nil
		},
	}
	ctx := context.Background()
	v1 := canonkey.Request{Tenant: "acme", ConfigVersion: "cfg-v1", Params: params}
	v2 := canonkey.Request{Tenant: "acme", ConfigVersion: "cfg-v2", Params: params}

	r1, _ := res.Resolve(ctx, v1)
	fmt.Printf("resolve(cfg-v1) -> %v (source calls: %d)\n", r1, calls)
	r2, _ := res.Resolve(ctx, v1)
	fmt.Printf("resolve(cfg-v1) -> %v (source calls: %d, cache hit)\n", r2, calls)
	r3, _ := res.Resolve(ctx, v2)
	fmt.Printf("resolve(cfg-v2) -> %v (source calls: %d, old key isolated)\n", r3, calls)
	r4, _ := res.Resolve(ctx, v1)
	fmt.Printf("resolve(cfg-v1) -> %v (old entry still intact)\n", r4)
}
