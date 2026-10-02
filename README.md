# crdt — JSON CRDT 层次对象协同解析与变更高阶树 (Go)

仅依赖 Go 标准库的可组合 JSON CRDT：任意嵌套的 **Map / Array / Register**
混合结构，支持多副本并发编辑后的精准三向合并（共同祖先 + 两个并发分支）。

## 设计

- **Op-based CRDT (CmRDT)**：每次变更产生一条 `Op` 进入因果日志，副本间通过
  交换/重放操作收敛。所有操作幂等、可交换，`Merge` 可以任意顺序、任意次数调用。
- **节点有稳定身份**：复合节点（Map/Array）创建一次、原地变更。并发修改同一
  列表项的不同字段时，合并在原节点上进行，**不会替换列表项的内存对象引用**。
- **冲突规则**：
  - Map：按 key 的 LWW（`(lamport, replica)` 定序）；
  - Array：RGA，插入锚定在元素 id 之后，并发插入按 `(lamport, id)` 确定性排序；
  - Register：LWW。
- **因果缓冲**：依赖未满足的操作（如父节点/锚点尚未到达）先挂起，依赖补齐后
  自动应用，因此操作可以乱序投递。

## API 速览

```go
a, b := crdt.New("alice"), crdt.New("bob")

a.Root().Set("list", []any{
    map[string]any{"title": "old", "tags": []any{"json"}},
})
b.Merge(a) // 同步共同祖先

// 并发编辑同一列表项的不同字段（期间不同步）
itemA, _ := a.Root().Get("list")
title, _ := itemA.Index(0).Get("title")
title.SetValue("协同编辑")        // alice: root.list[0].title = ...

itemB, _ := b.Root().Get("list")
tags, _ := itemB.Index(0).Get("tags")
tags.Push("go")                   // bob: root.list[0].tags.push("go")

a.Merge(b); b.Merge(a)            // 三向合并：两个更新都保留，副本收敛
```

`Ref` 是指向节点的稳定句柄：`Set/Get/Map/Array`（Map）、`Push/Index/Len`
（Array）、`SetValue/Value`（Register）、`ToJSON/String`（物化）。

## 运行样例

```
$ go run ./cmd/demo
共同祖先状态:
  alice: {"list":[{"tags":["json"],"title":"old"}]}
  bob:   {"list":[{"tags":["json"],"title":"old"}]}

合并后 (三向合并: 共同祖先 + 两个并发分支):
  alice: {"list":[{"tags":["json","go"],"title":"协同编辑"}]}
  bob:   {"list":[{"tags":["json","go"],"title":"协同编辑"}]}

list[0] 节点 id 合并前后保持稳定: alice:3
```

## 测试结果

```
$ go test ./... -v
=== RUN   TestConcurrentListItemFields      --- PASS   # 验收边界：并发改 title 与 tags.push，合并后均生效且节点引用未被覆盖
=== RUN   TestThreeWayMerge                 --- PASS   # 三副本三向合并收敛
=== RUN   TestConcurrentArrayPush           --- PASS   # 并发 push 均保留、顺序确定
=== RUN   TestRegisterLWW                   --- PASS   # 寄存器并发写 LWW 收敛
=== RUN   TestMapKeyLWW                     --- PASS   # 同 key 并发 Set 确定性收敛
=== RUN   TestDeepNestedConcurrentEdits     --- PASS   # 深层混合嵌套多层级并发编辑
=== RUN   TestMergeIdempotent               --- PASS   # 重复合并幂等
=== RUN   TestOutOfOrderOpDelivery          --- PASS   # 乱序投递仍收敛（因果缓冲）
=== RUN   TestTransitiveSync                --- PASS   # 间接 gossip 同步收敛
=== RUN   Example                           --- PASS
ok      crdt
```

## 文件

- `crdt.go` — 节点模型、操作日志、合并/因果缓冲、RGA/LWW 变更逻辑
- `ref.go` — `Ref` 句柄 API（Map/Array/Register 读写与物化）
- `crdt_test.go` / `example_test.go` — 测试与可运行示例
- `cmd/demo/main.go` — 双副本协同演示程序
