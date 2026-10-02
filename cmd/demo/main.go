// demo 演示标签联合编解码：schema 校验、显式演进、未知标签透传、批量原子验证。
package main

import (
	"bytes"
	"fmt"
	"log"

	"tagunion/tagunion"
)

const (
	tagTransfer = 1
	tagPing     = 2
)

func registry() *tagunion.Registry {
	r := tagunion.NewRegistry()
	must(r.Register(tagunion.Schema{Tag: tagTransfer, Version: 1, Fields: []tagunion.FieldDef{
		{Name: "amount", Type: tagunion.FUint},
	}}))
	must(r.Register(tagunion.Schema{Tag: tagTransfer, Version: 2, Fields: []tagunion.FieldDef{
		{Name: "amount", Type: tagunion.FUint},
		{Name: "currency", Type: tagunion.FString},
	}}))
	// 显式演进映射：v1 -> v2 补充 currency 字段。
	must(r.RegisterMigration(tagTransfer, 1, func(f map[string]any) (map[string]any, error) {
		f["currency"] = "USD"
		return f, nil
	}))
	// 空分支：合法，无字段。
	must(r.Register(tagunion.Schema{Tag: tagPing, Version: 1}))
	return r
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	r := registry()

	fmt.Println("== 1. 已知标签编码 -> 解码（v1 数据显式迁移到 v2）")
	f1, err := r.Encode(tagTransfer, 1, map[string]any{"amount": uint64(4200)})
	must(err)
	v1, n, err := r.Decode(f1)
	must(err)
	fmt.Printf("   encoded %d bytes, decoded tag=%d version=%d fields=%v\n", n, v1.Tag, v1.Version, v1.Fields)

	fmt.Println("== 2. 标签 A 搭标签 B 的字段 -> 拒绝")
	_, err = r.Encode(tagTransfer, 2, map[string]any{"amount": uint64(1), "currency": "USD", "op": "noop"})
	fmt.Printf("   rejected: %v\n", err)
	_, err = r.Encode(tagPing, 1, map[string]any{"amount": uint64(1)})
	fmt.Printf("   rejected: %v\n", err)

	fmt.Println("== 3. 空分支合法")
	fp, err := r.Encode(tagPing, 1, map[string]any{})
	must(err)
	vp, _, err := r.Decode(fp)
	must(err)
	fmt.Printf("   ping round-trip ok: tag=%d version=%d fields=%v\n", vp.Tag, vp.Version, vp.Fields)

	fmt.Println("== 4. 未知标签：不假装理解，版本与校验保留，重编码逐字节一致")
	// 构造未知标签 99，其载荷“巧合地”是一段合法的 transfer v1 载荷。
	coincident, err := r.Encode(tagTransfer, 1, map[string]any{"amount": uint64(7)})
	must(err)
	innerPayload := coincident[3 : len(coincident)-4] // 去掉帧头与校验，只取载荷
	fu, err := tagunion.EncodeUnknown(99, 5, innerPayload)
	must(err)
	vu, _, err := r.Decode(fu)
	must(err)
	re, err := r.Marshal(vu)
	must(err)
	fmt.Printf("   kind=Unknown tag=%d version=%d raw=%v byte-identical=%v\n",
		vu.Tag, vu.Version, vu.Raw, bytes.Equal(fu, re))

	fmt.Println("== 5. 批量原子验证：任一对象损坏则整体失败，定位路径与偏移")
	good1, _ := r.Encode(tagTransfer, 2, map[string]any{"amount": uint64(9), "currency": "EUR"})
	good2, _ := r.Encode(tagPing, 1, map[string]any{})
	batch, err := tagunion.EncodeBatch(good1, good2, fu)
	must(err)
	vals, err := r.DecodeBatch(batch)
	must(err)
	fmt.Printf("   ok: %d objects decoded atomically\n", len(vals))

	bad := append([]byte(nil), batch...)
	bad[len(good1)+2] ^= 0xFF // 破坏 obj[1]
	if _, err = r.DecodeBatch(bad); err != nil {
		fmt.Printf("   rejected: %v\n", err)
	}

	fmt.Println("== 6. 先验证长度再分配：巨型长度前缀直接拒绝")
	huge := []byte{0x01, 0x01, 0x80, 0x80, 0x80, 0x10} // tag=1 version=1 length=2^25
	if _, _, err = r.Decode(huge); err != nil {
		fmt.Printf("   rejected: %v\n", err)
	}
}
