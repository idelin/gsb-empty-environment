# graphdec

纯数据对象图的安全解码器（仅 Go 标准库）。解码在**深度、节点数、引用数、总字节**四个维度上联合限额，循环引用不会无限增加深度，任何错误都不会泄漏部分解码的对象。

## 支持的序列化范围

自定义二进制格式（`OGD1`），仅承载数据，不包含类型名、构造器或回调，**解码绝不执行宿主代码**。所有整数为 uvarint（`KindInt` 为 zig-zag varint）：

```
stream := magic rootID nodeCount node...
magic  := "OGD1"
node   := id kind payload        // id 在流内唯一，可按任意顺序出现
payload:= Null | Bool | Int | String | Bytes | List(childID...) | Map(keyID valueID...)
```

- 节点类型：`null / bool / int64 / string / bytes / list / map`。
- 允许前向引用（未声明的 id 暂存，受 `MaxPendingRefs` 限制，流结束必须全部解析）。
- 共享节点只出现一次（按实体计数一次）；循环用 id 回指表达。
- 所有长度字段在分配前先与剩余输入和预算比对；流尾多余字节、未知 kind、重复 id、悬空引用、不可达节点一律拒绝。

## 限额（`Limits`，零值字段取默认值）

| 字段 | 含义 | 默认 |
|---|---|---|
| `MaxBytes` | 输入总字节 | 1 MiB |
| `MaxNodes` | 不同节点数（共享只计一次） | 65536 |
| `MaxRefs` | 引用（边）总数，小输入大量引用同样受限 | 131072 |
| `MaxDepth` | 根可达 SCC 缩点图上的最长路径 | 256 |
| `MaxCycleNodes` | 参与循环的节点数（非平凡 SCC + 自环） | 1024 |
| `MaxPendingRefs` | 增量解码中暂存的未解析前向引用数 | 16384 |

## 关键设计

- **深度定义**：对根可达子图做 SCC 缩点（Kosaraju，全迭代实现，20 万深链不爆栈），深度 = 缩点 DAG 上的最长路径。循环不抬高深度；循环内部节点由 `MaxCycleNodes` 单独约束，结果不依赖 DFS 顺序。
- **独立分析器**：`analyze.go` 在解析后独立做引用解析、根可达性、深度与循环额度检查；恶意循环、巨大扇出、非法偏移（悬空 id）都在发布前被拦截。
- **原子发布**：先解析到私有暂存区，全部校验通过后才构建并返回 `*Value`；任何错误返回 `nil` + error，不泄漏部分对象。
- **无状态恢复**：`Decoder` 调用间不保存状态，限额拒绝后新调用可正常使用。

## 运行

```
go test ./...
go run ./cmd/sample
```

样例输出：

```
encoded: 102 bytes
decoded graph:
#0 map
  #1 string "title"
  #2 string "limits"
  #3 string "version"
  #4 int 3
  #5 string "draft"
  #6 bool false
  #7 string "author"
  #8 string "ada"
  #9 string "reviewer"
  -> ref #8
  #10 string "related"
  #11 list
    -> ref #0
deep chain rejected (value leaked: false): graphdec: condensation depth 101 exceeds depth budget 16
huge fan-out rejected (5012 bytes input, value leaked: false): graphdec: node 0 declares 5000 children, reference budget 2000 would be exceeded
decoder still usable after rejected inputs: ok
```

测试结果：22 个测试全部通过（`ok graphdec`），覆盖往返编解码、深度/节点/引用/字节限额、循环额度、自环、共享节点计数、截断输入、非法偏移、重复 id、不可达节点、声明长度预检、前向引用暂存上限、限额恢复与 20 万深链迭代分析。
