# graphser

序列化带共享引用与循环的 Go 对象图，解码后保持对象身份。仅使用标准库。

## 支持的序列化范围

| 类型 | 语义 |
|---|---|
| bool / string / 所有有符号、无符号整数 / float32、float64 | 值 |
| struct（仅导出字段，未导出字段跳过） | 值 |
| 指针（可为 nil） | **身份**：指向的对象是带 ID 的对象 |
| map（可为 nil，键必须是标量） | **身份**：map 本身是带 ID 的对象 |
| slice / array | 值（内联展开，不共享身份）；nil slice 与空 slice 可区分 |

不支持：interface、chan、func、unsafe.Pointer、复数、非标量 map 键 —— 编码时报错。
非有限浮点（NaN/Inf）由 encoding/json 拒绝。

**值相等 ≠ 对象相同**：两个字段指向同一指针，解码后仍是同一对象（`out.A == out.B`）；
两个值相等但各自独立的指针，解码后依然独立（`DeepEqual` 为真但 `==` 为假）。
自环（`n.Next = n`、`m["self"] = m`）原样保留。

## 线格式

JSON 文档 `{version, root, objects}`。每个不同的指针/map 按**首次遇到的 DFS 顺序**
（map 键排序，字段按声明序）分配从 1 开始的**稳定对象 ID**，同一图多次编码字节完全一致。
引用写作 `{"kind":"ref","ref":<id>}`，对象本体只在 `objects` 里出现一次。

## 解码安全性

- **不执行宿主代码**：只通过 `reflect.New`/`Set` 构造输出类型可达的类型；
  不调用任何方法、不走 `UnmarshalJSON` 钩子、不实例化接口；文档里的类型名只做一致性校验，不做解析执行。
- **先验证长度再分配**，整个文档在分配任何图内存之前完成校验：
  版本、重复对象 ID、悬空引用、重复字段名、未知 kind、深度与长度上限
  （`maxObjects` / `maxLen` / `maxDepth`）。
- **四阶段解码**：validate → resolve（按输出类型推导每个 ID 的唯一期望类型，冲突即拒绝）
  → allocate（先建零值占位：`reflect.New` / `MakeMapWithSize`）→ fill（填充并连接引用，
  共享对象只有一份身份，环正确闭合）。
- 拒绝：悬空 ID、重复定义、同一 ID 类型冲突、定义了却从未被引用的对象、
  数组长度不符、整数溢出、版本不符、尾部垃圾数据。

## 用法

```go
data, err := graphser.Marshal(v)        // v 为任意受支持类型的值
err = graphser.Unmarshal(data, &out)    // out 类型须与编码时一致（指针对指针）
```

## 运行样例

```
$ go run ./cmd/graphdemo
=== wire format (note: object 1 appears once, referenced 3x) ===
{
  "version": 1,
  "root": { "kind": "ref", "ref": 1 },
  "objects": [
    { "id": 1, "type": "main.Graph", "value": { ... "First": {"kind":"ref","ref":2},
        "Second": {"kind":"ref","ref":2}, "Loop": {"kind":"ref","ref":3}, ... } },
    { "id": 2, "type": "main.Node", "value": { ... "Name": "shared" ... } },
    { "id": 3, "type": "main.Node", "value": { ... "Next": {"kind":"ref","ref":3} } },
    { "id": 4, "type": "map[string]*main.Node", "value": { ... "again": {"kind":"ref","ref":2} } }
  ]
}
=== identity after decode ===
First == Second:         true
First == Index[again]:   true
Loop.Next == Loop:       true
First != Loop:           true
=== value equality vs identity ===
DeepEqual(*First, *Second): true
First == Second:             false
=== malformed documents are rejected ===
rejected: graphser: dangling reference to object 9
rejected: graphser: duplicate definition of object 1
```

（对象 2 被 `First`、`Second`、`Index["again"]` 三处引用，本体只存一次；
对象 3 的 `Next` 引用自身，即自环。）

## 测试结果

```
$ go test -v ./...
--- PASS: TestSharedIdentityPreserved        # 两处引用同对象，恢复后仍同身份
--- PASS: TestSelfCyclePreserved             # 自环保留
--- PASS: TestTwoNodeCycle                   # 双节点环
--- PASS: TestMapIdentityAndSelfCycle        # map 自引用
--- PASS: TestSharedMapIdentity              # 共享 map 身份
--- PASS: TestValueEqualityVsIdentity        # 值相等 ≠ 对象相同
--- PASS: TestStableIDsAndDeterministicBytes # 稳定 ID、输出字节确定
--- PASS: TestRoundTripValues                # 标量/slice/array/nil 往返
--- PASS: TestDanglingRefRejected            # 悬空 ID 拒绝
--- PASS: TestDuplicateIDRejected            # 重复定义拒绝
--- PASS: TestConflictingTypesRejected       # 同 ID 类型冲突拒绝
--- PASS: TestUnreferencedObjectRejected     # 未被引用的定义拒绝
--- PASS: TestDuplicateFieldRejected         # 重复字段拒绝
--- PASS: TestLengthValidatedBeforeAllocation# 先验证长度再分配
--- PASS: TestDepthLimit                     # 深度上限
--- PASS: TestOverflowRejected               # 整数溢出拒绝
--- PASS: TestTypeMismatchRejected           # 类型名不符拒绝
--- PASS: TestUnsupportedTypesRejected       # interface/chan/func 拒绝
--- PASS: TestTrailingDataRejected           # 尾部垃圾拒绝
--- PASS: TestWrongVersionRejected           # 版本不符拒绝
--- PASS: TestPointerToMap                   # 指向 map 的指针
PASS
ok  	graphser
```
