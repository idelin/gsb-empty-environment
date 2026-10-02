package jsonproj

import (
	"strings"
	"testing"
)

// orderJSON is a complex nested business order: user profile, shipping
// address, a large item list, payment and logistics metadata.
const orderJSON = `{
  "orderId": "SO-2026-1008611",
  "channel": "app",
  "user": {
    "id": 88421,
    "name": "张伟",
    "vip": true,
    "address": {
      "country": "CN",
      "province": "上海",
      "city": "上海市",
      "street": "世纪大道 100 号",
      "geo": {"lat": 31.2304, "lng": 121.4737}
    },
    "tags": ["whale", "returning"]
  },
  "items": [
    {"sku": "A-1001", "name": "机械键盘", "qty": 1, "price": 399.00,
     "attrs": {"color": "black", "switch": "linear"}},
    {"sku": "B-2032", "name": "人体工学椅", "qty": 2, "price": 1299.50,
     "attrs": {"color": "gray", "material": "mesh"}},
    {"sku": "C-3300", "name": "显示器挂灯", "qty": 1, "price": 199.00,
     "attrs": {"color": "silver", "power": "usb-c"}}
  ],
  "payment": {
    "method": "alipay",
    "currency": "CNY",
    "total": 3197.00,
    "installments": {"count": 3, "interest": 0}
  },
  "logistics": {
    "carrier": "顺丰",
    "tracking": ["SF1001", "SF1002"],
    "warehouse": {"id": "WH-SH-01", "city": "上海市"}
  },
  "coupon": null,
  "remark": {}
}`

func TestInferSchema(t *testing.T) {
	paths, err := InferSchema(strings.NewReader(orderJSON))
	if err != nil {
		t.Fatalf("InferSchema: %v", err)
	}
	got := map[string]bool{}
	for _, p := range paths {
		got[p] = true
	}
	for _, want := range []string{
		"orderId",
		"user.address.city",
		"user.address.geo.lat",
		"user.tags[0]",
		"items[0].sku",
		"items[2].attrs.power",
		"payment.installments.count",
		"logistics.warehouse.city",
		"coupon",
		"remark",
	} {
		if !got[want] {
			t.Errorf("schema missing path %q", want)
		}
	}
	if got["items"] || got["user.address"] {
		t.Errorf("schema should only contain leaf paths, got: %v", paths)
	}
}

func TestProjectCityColumn(t *testing.T) {
	proj := NewProjector("user.address.city")
	cols, err := proj.Project(strings.NewReader(orderJSON))
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(cols) != 1 {
		t.Fatalf("expected exactly 1 virtual column, got %d", len(cols))
	}
	col := cols["user.address.city"]
	if col == nil {
		t.Fatal("missing column user.address.city")
	}
	if len(col.Values) != 1 || col.Values[0] != "上海市" {
		t.Fatalf("unexpected city column values: %v", col.Values)
	}
}

func TestProjectDoesNotTouchUnrelatedSubtrees(t *testing.T) {
	// The items list contains a value that is valid as a raw JSON token
	// stream but would change the result if it were deserialized: the
	// projector must skip it at the token level and only surface the city.
	doc := `{"user":{"address":{"city":"杭州市"}},"items":[` + strings.Repeat(`{"sku":"X","price":9.9},`, 999) + `{"sku":"X","price":9.9}]}`
	proj := NewProjector("user.address.city")
	cols, err := proj.Project(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	col := cols["user.address.city"]
	if len(col.Values) != 1 || col.Values[0] != "杭州市" {
		t.Fatalf("unexpected values: %v", col.Values)
	}
}

func TestProjectMultiplePathsWithArrays(t *testing.T) {
	proj := NewProjector("items[0].sku", "items[1].price", "logistics.warehouse.city", "payment.total")
	cols, err := proj.Project(strings.NewReader(orderJSON))
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	cases := map[string][]any{
		"items[0].sku":            {"A-1001"},
		"items[1].price":          {1299.50},
		"logistics.warehouse.city": {"上海市"},
		"payment.total":           {3197.00},
	}
	for path, want := range cases {
		col := cols[path]
		if col == nil {
			t.Fatalf("missing column %q", path)
		}
		if len(col.Values) != len(want) {
			t.Fatalf("%s: want %d values, got %v", path, len(want), col.Values)
		}
		for i, w := range want {
			if col.Values[i] != w {
				t.Errorf("%s[%d]: want %v (%T), got %v (%T)", path, i, w, w, col.Values[i], col.Values[i])
			}
		}
	}
}

func TestProjectMissingPathYieldsEmptyColumn(t *testing.T) {
	proj := NewProjector("user.address.city", "user.address.zipcode")
	cols, err := proj.Project(strings.NewReader(orderJSON))
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got := cols["user.address.zipcode"].Values; len(got) != 0 {
		t.Fatalf("expected empty column for missing path, got %v", got)
	}
	if got := cols["user.address.city"].Values; len(got) != 1 {
		t.Fatalf("expected city to be captured, got %v", got)
	}
}

func TestProjectPrefixBoundary(t *testing.T) {
	// "user.name" must not be confused with a sibling key "user.nameFirst".
	doc := `{"user":{"nameFirst":"张","name":"张伟","address":{"city":"北京市"}}}`
	proj := NewProjector("user.name")
	cols, err := proj.Project(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	got := cols["user.name"].Values
	if len(got) != 1 || got[0] != "张伟" {
		t.Fatalf("unexpected values: %v", got)
	}
}

func TestProjectInvalidJSON(t *testing.T) {
	proj := NewProjector("a.b")
	if _, err := proj.Project(strings.NewReader(`{"a": `)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func BenchmarkProjectCityOnly(b *testing.B) {
	for i := 0; i < b.N; i++ {
		proj := NewProjector("user.address.city")
		if _, err := proj.Project(strings.NewReader(orderJSON)); err != nil {
			b.Fatal(err)
		}
	}
}
