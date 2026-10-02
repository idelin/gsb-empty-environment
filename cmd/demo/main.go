// Demo: flatten a complex nested order JSON into virtual columns, then
// project only the city dimension without deserializing the item list.
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"jsonproj"
)

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
  "payment": {"method": "alipay", "currency": "CNY", "total": 3197.00,
              "installments": {"count": 3, "interest": 0}},
  "logistics": {"carrier": "顺丰", "tracking": ["SF1001", "SF1002"],
                "warehouse": {"id": "WH-SH-01", "city": "上海市"}},
  "coupon": null,
  "remark": {}
}`

func main() {
	// 1. Infer the leaf-path schema of the semi-structured document.
	paths, err := jsonproj.InferSchema(strings.NewReader(orderJSON))
	if err != nil {
		panic(err)
	}
	fmt.Printf("inferred %d leaf paths, e.g.:\n", len(paths))
	for _, p := range paths[:8] {
		fmt.Printf("  %s\n", p)
	}
	fmt.Println("  ...")

	// 2. Project only the city dimension into its own virtual column.
	//    The items list, payment and logistics subtrees are skipped as
	//    raw tokens and never deserialized.
	proj := jsonproj.NewProjector("user.address.city")
	cols, err := proj.Project(strings.NewReader(orderJSON))
	if err != nil {
		panic(err)
	}
	out, _ := json.Marshal(cols["user.address.city"].Values)
	fmt.Printf("\nprojected column user.address.city = %s\n", out)

	// 3. Project a few more leaf paths on demand.
	proj = jsonproj.NewProjector("items[1].price", "logistics.warehouse.city", "payment.total")
	cols, err = proj.Project(strings.NewReader(orderJSON))
	if err != nil {
		panic(err)
	}
	fmt.Println("\nprojected columns:")
	for _, path := range proj.Paths() {
		v, _ := json.Marshal(cols[path].Values)
		fmt.Printf("  %-26s = %s\n", path, v)
	}
}
