# jsoncrdt — JSON CRDT 层次对象协同解析与变更高阶树 (Go)

纯标准库实现的复合 JSON CRDT，支持 `Map` / `Array` / `Register` 任意混合嵌套，
并发修改不同层级属性时可精准三向合并（state-based join，满足交换律/结合律/幂等）。

## 数据模型

| 节点       | 语义                                                                 |
|------------|----------------------------------------------------------------------|
| `Map`      | 每个 key 一次 LWW 写（含删除墓碑）；同源的 key 递归就地合并子树        |
| `Array`    | RGA：元素带全局唯一 ID + prev 链，并发插入确定性排序，删除为墓碑       |
| `Register` | LWW 寄存器，时间戳为 `(Lamport counter, replica ID)`                  |

关键性质：**合并共享子树时原地归并，不替换既有 Go 对象引用**——只有对端对同一
key 的整键覆写（更新的 LWW 写）才会替换节点。

## 用法

```go
base := crdt.NewDoc("base")
item := base.Root().PutArray("list").PushMap()
item.PutRegister("title", "old title")
item.PutArray("tags")

a, b := base.Fork("A"), base.Fork("B")

// 并发修改不同层级
a.Root().Get("list").(*crdt.Array).At(0).(*crdt.Map).
    Get("title").(*crdt.Register).Set("new title")   // root.list[0].title
b.Root().Get("list").(*crdt.Array).At(0).(*crdt.Map).
    Get("tags").(*crdt.Array).PushRegister("go")     // root.list[0].tags.push

a.Merge(b)
b.Merge(a) // 双向合并后两副本收敛，list[0] 的 *Map 指针不变
```

## 运行

```sh
go test ./...      # 全部测试（含验收场景）
go test -run TestConcurrentNestedEditsMergePrecisely -v .
go run ./cmd/demo  # 运行样例：并发编辑 → 合并 → 收敛
```

## 文件

- `crdt.go` — 核心实现（Doc / Map / Array / Register / Merge / Fork）
- `crdt_test.go` — 验收测试 + 合并语义测试（LWW、RGA、删除、幂等/交换律）
- `example_test.go` — 可运行的 Example（`go test` 自动校验输出）
- `cmd/demo/main.go` — 命令行演示
