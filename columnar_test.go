package columnar

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

// orderJSON 生成一篇复杂嵌套的业务订单 JSON。
// items 列表故意放大，模拟“订单商品列表”这种与维度分析无关的大结构。
func orderJSON(id string, city string, items int) string {
	var b strings.Builder
	b.WriteString(`{
  "id": "` + id + `",
  "paid": true,
  "user": {
    "name": "alice",
    "address": { "city": "` + city + `", "zip": "100000" }
  },
  "tags": ["vip", "wholesale"],
  "items": [`)
	for i := 0; i < items; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"sku":"SKU-%d","price":%d,"attrs":{"color":"red","size":"L"}}`, i, 10+i)
	}
	b.WriteString(`],
  "payment": { "method": "card", "detail": { "last4": "4242", "exp": "12/30" } }
}`)
	return b.String()
}

func docs(d ...string) io.Reader { return strings.NewReader(strings.Join(d, "\n")) }

func TestFlattenInfersPaths(t *testing.T) {
	store, err := Flatten(docs(orderJSON("A1", "Beijing", 3)))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	want := []string{
		"id",
		"items[].attrs.color",
		"items[].attrs.size",
		"items[].price",
		"items[].sku",
		"paid",
		"payment.detail.exp",
		"payment.detail.last4",
		"payment.method",
		"tags[]",
		"user.address.city",
		"user.address.zip",
		"user.name",
	}
	if got := store.Paths(); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths mismatch:\n got=%v\nwant=%v", got, want)
	}
	if store.Rows() != 1 {
		t.Fatalf("rows=%d, want 1", store.Rows())
	}
	city, ok := store.Column("user.address.city")
	if !ok || city[0] != "Beijing" {
		t.Fatalf("city column = %v, ok=%v", city, ok)
	}
	prices := store.MustColumn("items[].price")[0].([]any)
	if len(prices) != 3 || prices[0].(json.Number).String() != "10" {
		t.Fatalf("items[].price = %v", prices)
	}
}

func TestProjectCityOnlySkipsUnrelated(t *testing.T) {
	// 3 篇订单，每篇带 200 个商品的大 items 列表
	input := docs(
		orderJSON("A1", "Beijing", 200),
		orderJSON("A2", "Shanghai", 200),
		orderJSON("A3", "Beijing", 200),
	)
	store, stats, err := Project(input, []string{"user.address.city"})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}

	// 只产生目标列
	if got := store.Paths(); !reflect.DeepEqual(got, []string{"user.address.city"}) {
		t.Fatalf("projected paths = %v", got)
	}
	col := store.MustColumn("user.address.city")
	if got := []any{"Beijing", "Shanghai", "Beijing"}; !reflect.DeepEqual(col, got) {
		t.Fatalf("city column = %v, want %v", col, got)
	}

	// 验证“只读相关列”：只物化 3 个值，其余 token 全部词法跳过
	if stats.ValuesCaptured != 3 {
		t.Fatalf("ValuesCaptured = %d, want 3", stats.ValuesCaptured)
	}
	if stats.TokensSkipped == 0 {
		t.Fatal("expected tokens to be skipped")
	}
	var skippedItems bool
	for _, p := range stats.SkippedSubtrees {
		if p == "items" {
			skippedItems = true
		}
	}
	if !skippedItems {
		t.Fatalf("items subtree was not skipped: %v", stats.SkippedSubtrees)
	}
	t.Logf("docs=%d captured=%d skippedTokens=%d skippedSubtrees=%v",
		stats.Docs, stats.ValuesCaptured, stats.TokensSkipped, stats.SkippedSubtrees)
}

func TestProjectMatchesFlattenColumn(t *testing.T) {
	input := orderJSON("A1", "Beijing", 5) + "\n" + orderJSON("A2", "Shanghai", 5)

	flat, err := Flatten(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	proj, _, err := Project(strings.NewReader(input), []string{"user.address.city"})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if !reflect.DeepEqual(flat.MustColumn("user.address.city"), proj.MustColumn("user.address.city")) {
		t.Fatalf("projection mismatch: flat=%v proj=%v",
			flat.MustColumn("user.address.city"), proj.MustColumn("user.address.city"))
	}
}

func TestProjectArrayLeaf(t *testing.T) {
	store, _, err := Project(docs(orderJSON("A1", "Beijing", 3)), []string{"items[].price"})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	col := store.MustColumn("items[].price")
	if len(col) != 1 {
		t.Fatalf("rows = %d, want 1", len(col))
	}
	prices := col[0].([]any)
	if len(prices) != 3 || prices[2].(json.Number).String() != "12" {
		t.Fatalf("items[].price = %v", prices)
	}
}

func TestMissingPathFilledWithNil(t *testing.T) {
	input := `{"user":{"address":{"city":"Beijing"}}}` + "\n" + `{"user":{"name":"bob"}}`
	store, err := Flatten(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	city := store.MustColumn("user.address.city")
	if city[0] != "Beijing" || city[1] != nil {
		t.Fatalf("city column = %v", city)
	}
	name := store.MustColumn("user.name")
	if name[0] != nil || name[1] != "bob" {
		t.Fatalf("name column = %v", name)
	}
}

func TestInvalidJSONReturnsError(t *testing.T) {
	if _, err := Flatten(strings.NewReader(`{"a": 1,}`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if _, _, err := Project(strings.NewReader(`{"a":`), []string{"a"}); err == nil {
		t.Fatal("expected error for truncated JSON")
	}
}
