# testselector

基于符号调用图的变更影响测试选择器。输入旧/新两份调用图（JSON），结合图 diff
与显式变更符号，反向传播影响，选出可能受影响的测试，并给出每个测试的
**最短影响路径**。

## 用法

```sh
go run ./cmd/testselector -old examples/old.json -new examples/new.json \
    [-changed pkg.Foo,pkg.Bar] [-format text|json]
go test ./...
```

无选中测试时退出码为 1，便于接入 CI 门禁。

## 调用图模型

每条边是三种形态之一（见 `selector/graph.go`）：

- **静态调用**：`{"caller": "a.F", "callee": "b.G"}`
- **动态调用**：候选集表示，`{"caller": "a.F", "dynamic": true, "candidates": ["x.H", "y.H"]}`，
  任一候选变更都会沿该边传播影响
- **未解析调用**：`{"caller": "a.F", "unresolved": true, "name": "H"}`，
  按显式保守规则展开（见下）

## 未解析调用的保守展开规则

按顺序取第一条命中的规则（`ExpandEdge`）：

1. 展开为符号表中所有**短名相同**的符号；
2. 若无匹配，展开为**调用方同包**的全部符号；
3. 若同包为空，展开为**全图**所有符号。

## 变更集计算（`ChangedSymbols`）

- `explicit`：命令行显式指定（如来自 git diff）；
- `deleted`：旧图有、新图无的符号（删除符号本身即影响源，沿旧边反向传播）；
- `added`：新图有、旧图无的符号；
- `edges-changed`：新旧两图中**展开后出边目标集不同**的符号
  （含新增首条出边、出边被删光两种情况）。

## 影响传播与最短路径

- 在**旧图 ∪ 新图**的反向边（callee → callers）上做多源 BFS：被删除的边
  仍标识了旧的依赖关系，删除符号的旧调用方因此仍能被触达；
- 递归调用形成的环由 visited 标记自然处理，BFS 保证每个测试拿到的是
  最短影响路径（`root -> ... -> test`）；
- 测试识别：`kind == "test"` 或短名以 `Test` 开头，且必须存在于新图中；
- 无法到达任何测试的变更符号列入 `uncovered`，提示测试覆盖空洞。

## 测试覆盖点（`selector/selector_test.go`）

- 删除符号沿旧边影响旧调用方；
- 递归调用成环时 BFS 终止且取最短路径；
- 旧图到新图的边变化（换边、删边、新增首条出边）；
- 动态调用候选集传播；
- 未解析调用三条保守展开规则；
- 菱形图上最短路径选择、变更符号本身是测试、无覆盖报告。
