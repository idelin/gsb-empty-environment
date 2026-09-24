# enumkit — 稳定 ID + 版本化别名的枚举库（Go 标准库）

空仓库从零实现。每个枚举成员拥有**稳定数值 ID**（序列化身份，永不改变）与**随版本变化的名称**；旧名可作为别名继续接受，未知值词法透传。

## 语义规则

| 需求 | 实现 |
| --- | --- |
| 稳定数值 ID，名称随版本变化 | `Define(id, name)` + 每版本 `Version{Active, Aliases}`；`Rename` 只改当前名，不改 ID |
| 旧名别名 | 版本内 `Aliases`，`Decode(version, token)` 先按该版本名称/别名解析 |
| 同别名映到两 ID 非法 | 同一版本内名称→ID 必须唯一，冲突返回 `ConflictError` |
| 未知值透传 | 未知名 `UnknownName`、未知数值 `UnknownNumber`，`Raw` 保留原 token；未知数值**不强转首项** |
| 规范输出用当前名 | `Registry.Canonical(v)` 返回当前名；未知值返回原 token |
| 多版本图允许合并 | `Registry.Merge(...)` 合并所有名称↔ID 边 |
| 禁止两个不同稳定 ID 共享同一活跃名 | 合并时检出 `MergeConflictError`，附 BFS **最短冲突链** |
| 迁移遇歧义返回歧义 | 别名跨版本映到多个 ID 时图可合并；`Graph.Migrate` 返回 `AmbiguityError`（候选 ID + 最短链），绝不随意择一 |
| 历史 schema 快照不受别名撤回影响 | `Snapshot(ver)` 深拷贝；之后 `RetractAlias` 只影响实时视图 |
| 全路径核验迁移一致性 | `Graph.Verify()` 遍历所有版本对 × 源版本全部 token，前向迁移再迁回，要求 ID 不变 |
| 冲突链 + 原始字段路径 | 链：`#4 --"LIGHT"(v1:alias, v2:alias)--> #5`；JSON 迁移错误带路径，如 `spec.items[1].mode`（支持 `*` 段通配） |
| 重命名不改变序列化身份 | 二进制格式已知值编码为 uvarint ID |
| 自行定义序列化范围 | ID ∈ `[0, 2^31-1]`，token ≤ 256 字节 |
| 解码不执行宿主代码 | 纯字节解析，无 gob/反射/插件；JSON 仅收 string/number |
| 先验证长度再分配 | 长度前缀先与 `MaxTokenLen`、剩余缓冲比较，通过后才 `string(...)` 分配 |

## 二进制格式

- `0x01` + uvarint：已知值的稳定 ID
- `0x02` + uvarint(len) + len 字节：未知值的原始词法 token

## 运行

```sh
go test ./...        # 测试
go run ./cmd/demo    # 端到端样例
```

## 样例输出（`go run ./cmd/demo`）

```
== decode: old alias, canonical output, passthrough ==
v2 "GREEN"      -> id=2 canonical="EMERALD"
v3 "GREEN"      -> known=false raw="GREEN" (alias retracted, lexical passthrough)
v1 "99"         -> known=false raw="99" (unknown number, not coerced)

== snapshot survives alias retraction ==
snapshot(v2) GREEN -> id=2 ok=true (frozen)
live v2 GREEN      -> known=false (retracted)

== merge + migrate across versions ==
migrate v1->v3 "RED"   -> id=1 canonical@v3="CRIMSON"
migrate v1->v3 "GREEN" -> id=2 canonical@v3="EMERALD"
graph verify: 0 problems

== ambiguous migration is reported, never guessed ==
migrate "LIGHT": enum lamp: token "LIGHT" is ambiguous between stable IDs [4 5]; shortest conflict chain: #4 --"LIGHT"(v1:alias, v2:alias)--> #5
lamp graph verify: 4 problem(s), first: enum lamp: migration path v1->v1 token "LIGHT": enum lamp: token "LIGHT" is ambiguous between stable IDs [4 5]; shortest conflict chain: #4 --"LIGHT"(v1:alias, v2:alias)--> #5

== merge conflict: two IDs share one active name ==
merge rejected: enum bad: active name "GREEN" is shared by different stable IDs [1 2]; shortest conflict chain: #1 --"GREEN"(v2:alias, v1:active)--> #2

== safe codec: round-trip, rename-stable identity ==
wire(v1 RED) = 01 01
decoded id=1 canonical(now)="CRIMSON" (rename did not change identity)
oversized length prefix rejected before allocation: enumkit: token exceeds MaxTokenLen: declared 4294967295 > 256

== JSON document migration with original field paths ==
color migration: {"spec":{"items":[{"mode":"EMERALD"},{"mode":"LIGHT"}],"mode":"CRIMSON"}} (errs=[])
lamp migration error: spec.items[1].mode: enum lamp: token "LIGHT" is ambiguous between stable IDs [4 5]; shortest conflict chain: #4 --"LIGHT"(v1:alias, v2:alias)--> #5
```

## 测试结果（`go test ./...`）

27 个用例全部通过（Go 1.24.2，`gofmt`/`go vet` 干净），覆盖：

- 旧别名解析、当前名规范输出、未知名称/数值词法透传、未知数值不强转
- 版本内同别名映两 ID 拒绝、同 ID 重复别名接受、活跃名冲突拒绝
- 别名撤回后实时视图失效而快照不变、活跃名不可撤回
- 重命名后二进制回放身份（ID）不变
- 合并允许一致别名；两个 ID 共享活跃名拒绝并给出最短链
- 跨版本别名歧义迁移返回 `AmbiguityError`；`Verify` 全路径检出；多跳 BFS 最短链
- 二进制 round-trip、截断/坏 tag、超大长度前缀先拒后分配、范围上限、JSON 值
- JSON 文档按路径通配解码、歧义错误携带 `spec.items[1].mode` 原始路径、迁移改写规范名且未知值透传

## 文件

- `enum.go` — 注册表、版本、值、解码、重命名、快照
- `errors.go` — 冲突/歧义/路径错误与冲突链渲染
- `graph.go` — 别名图合并、BFS 最短链、迁移
- `verify.go` — 全路径迁移一致性核验
- `codec.go` — 安全二进制/JSON 编解码
- `doc.go` — JSON 文档字段路径解码与迁移
- `*_test.go` — 27 个 `go test` 用例
- `cmd/demo/main.go` — 端到端运行样例
