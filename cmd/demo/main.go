// demo 演示：v2 程序写入 -> v1 旧程序读取并转存（新字段不丢）-> v2 再读回。
package main

import (
	"fmt"

	wire "example.com/stablewire"
)

func personV1() *wire.Schema {
	s := wire.NewSchema("person", 1)
	s.MustAdd(wire.FieldDef{Num: 1, Type: wire.TString, Required: true}) // name
	s.MustAdd(wire.FieldDef{Num: 2, Type: wire.TUint64})                 // age
	return s
}

func personV2() *wire.Schema {
	s := wire.NewSchema("person", 2)
	s.MustAdd(wire.FieldDef{Num: 1, Type: wire.TString, Required: true})
	s.MustAdd(wire.FieldDef{Num: 2, Type: wire.TUint64})
	s.MustAdd(wire.FieldDef{Num: 3, Type: wire.TBool})  // vip（新增可选）
	s.MustAdd(wire.FieldDef{Num: 4, Type: wire.TBytes}) // avatar（新增可选）
	_ = s.Reserve(5, "nickname deleted in v2")          // 删除编号永久保留
	return s
}

func main() {
	// 1) 新程序（v2）写入一条记录。
	out := wire.NewMessage(personV2())
	out.Set(1, "alice")
	out.Set(2, uint64(30))
	out.Set(3, true)
	out.Set(4, []byte("PNG..."))
	data, _ := out.Encode()
	fmt.Printf("1) v2 encoded %d bytes: %x\n", len(data), data)

	// 2) 旧程序（v1 schema）解码：未知字段 3、4 被保留，并给出版本诊断。
	old, err := wire.Decode(personV1(), data)
	if err != nil {
		panic(err)
	}
	fmt.Printf("2) v1 decoded: name=%q age=%d\n   diagnostics: %s\n",
		old.GetString(1), old.GetUint64(2), old.Diag)

	// 3) 旧程序转存，新程序再读：新字段完整无损。
	resaved, _ := old.Encode()
	back, err := wire.Decode(personV2(), resaved)
	if err != nil {
		panic(err)
	}
	avatar, _ := back.Get(4)
	fmt.Printf("3) v1 resaved -> v2 decoded: vip=%v avatar=%q (unknown fields survived)\n",
		back.GetBool(3), avatar)

	// 4) 缺必填字段：报具体编号。
	broken := append([]byte(nil), data[:4]...) // 只留帧头
	if _, err := wire.Decode(personV2(), broken); err != nil {
		fmt.Printf("4) missing required -> %v\n", err)
	}

	// 5) 删除编号 5 禁止复用。
	if err := personV2().AddField(wire.FieldDef{Num: 5, Type: wire.TBytes}); err != nil {
		fmt.Printf("5) reuse deleted number -> %v\n", err)
	}
}
