# AW-LWW-Map (Go)

添加胜出的最后写入键值映射表（Add-Wins Last-Writer-Wins Map）CRDT，纯标准库实现。

## 语义

- 每个键维护**添加时间戳集合**与**删除时间戳集合**（Lamport 时钟 + 节点 ID 全序）。
- 键存活判定（添加胜出）：`max(adds) >= max(removes)`，时间戳相等时添加胜出。
- 写入嵌套字段时，路径上的每个键都会以当前时间戳**重新添加（touch）**：
  - 并发的「删除根键」与「更新键内字段」→ **更新优先**，键及字段保留；
  - 因果上晚于更新的删除（观察到更新后再删）→ 删除生效。
- 叶子值按 `(time, node)` 取最后写入；`Merge` 满足交换律、结合律、幂等律（CVRDT，收敛）。

## API

```go
m := awlwwmap.New("A")              // 节点 ID 全局唯一
m.Set(25, "user_profile", "age")    // 动态增/改子字段（自动创建中间层）
m.Remove("user_profile")            // 删除根键
m.Remove("user_profile", "age")     // 删除子字段
v, ok := m.Get("user_profile", "age")
m.Merge(other)                      // 合并副本，双向合并后收敛
snap := m.Snapshot()                // 导出存活内容为嵌套 map
```

## 运行

```sh
go test ./... -v    # 运行测试
go run ./example    # 运行验收场景样例
```

## 验收边界

节点 A 删除根键 `user_profile`，节点 B 并发修改 `user_profile.age=25`。
因果合并判定更新优先：合并后 `user_profile` 保留，`age=25`，且此前已复制的
`name` 字段不丢失。对应测试 `TestConcurrentRemoveVsNestedUpdate_UpdateWins`。

样例输出（`go run ./example`）：

```
== concurrent ops ==
A: Remove(user_profile)          // delete the root key
B: Set(user_profile.age = 25)    // update a nested field
== after merge (both nodes converge) ==
settings:
  theme: dark
user_profile:
  age: 25
  name: delin
user_profile survived: true, age=25 name=delin
```

## 测试结果

```
--- PASS: TestSetGetAndSnapshot
--- PASS: TestOverwriteIsLastWriterWins
--- PASS: TestRemoveRootKey
--- PASS: TestRemoveSubField
--- PASS: TestMergePropagatesRemove
--- PASS: TestConcurrentRemoveVsNestedUpdate_UpdateWins   # 验收边界
--- PASS: TestConcurrentRemoveVsUpdateTieAddWins
--- PASS: TestCausalRemoveBeatsEarlierUpdate
--- PASS: TestReAddAfterRemove
--- PASS: TestConcurrentLeafWritesConverge
--- PASS: TestMergeIsCommutativeAndIdempotent
--- PASS: TestNestedRemoveMergesWithSiblingUpdate
PASS
ok  	awlwwmap/awlwwmap
```
