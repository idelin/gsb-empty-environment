# 离线最小重建成本规划器（rebuildplanner）

给定**失效产物清单**与**可从缓存恢复的节点**，在本地包索引上计算满足全部构建目标的
**最小重建成本计划**。纯 Python 3.11 标准库实现，不联网查包，附 `unittest` 测试。

## 规则定义

1. **节点闭包**：只构建目标节点的传递依赖闭包，闭包外节点不参与计划。
2. **每个节点恰有一种动作**：
   - `reuse`（0 成本）：产物未失效，且其依赖都没有被重建。
   - `restore`（成本 = 缓存恢复报价）：节点产物已失效，但缓存条目存在且
     **摘要与节点 `digest` 完全一致**。
   - `rebuild`（成本 = `rebuild_cost`）：重新构建。
3. **缓存摘要不匹配（或缺失）= 不可恢复**，该失效节点必须 `rebuild`。
4. **失效传播**：重建一个节点会使其所有下游产物失效，下游必须一并 `rebuild`
   （即使下游自身缓存摘要匹配也不能复用——因为上游产物已变）；而 `restore`
   恢复出的产物与原产物内容一致，**不影响下游**。
5. **共享依赖只算一次**：成本按节点计。两个目标共享的昂贵中间产物（例如示例中的
   `core-2.0.0` 同时被 `web`、`api` 依赖）无论被几条路径需要，只计一次成本。
6. **版本**：`major.minor.patch[-prerelease][+build]`，遵循 SemVer 2.0.0 比较规则；
   约束支持 `^`、`~`、`>=`、`<=`、`>`、`<`、`=`/`==`、`!=` 与逗号组合；
   预发布版本只有在约束显式引用预发布时才允许匹配。
7. 节点使用的包与版本必须都存在于本地 `packages.json`，否则报错（不联网）。

优化问题为：对每个“可恢复的失效节点”选择 restore 或 rebuild（决策会经规则 4 传播），
使闭包内总成本最小。决策点 ≤ 20 时用**记忆化枚举求精确最优**；更大输入自动退化为
线性时间的拓扑贪心启发式（`planner.EXACT_LIMIT` 可调）。

## 输入格式

`packages.json`（本地包索引）：

```json
{
  "packages": {
    "web": {"versions": ["1.4.0", "1.5.0-rc.1", "1.5.0"]}
  }
}
```

`build.json`（构建输入，`nodes` 按依赖在前的拓扑顺序给出）：

```json
{
  "nodes": [
    {
      "id": "core-2.0.0",
      "package": "core",
      "version": "2.0.0",
      "digest": "sha256:core-artifact",
      "rebuild_cost": 100,
      "deps": [{"node": "util-1.0.0", "constraint": "^1.0.0, >=1.0.0"}]
    }
  ],
  "failed": ["core-2.0.0"],
  "cache": [{"node": "core-2.0.0", "digest": "sha256:core-artifact", "restore_cost": 20}],
  "targets": ["web-1.4.0"]
}
```

`cache` 条目省略 `restore_cost` 时默认为 0。输入校验包括：未知包/版本、
依赖约束不满足、前向依赖、依赖环、重复 id、未知目标等，均会得到明确报错。

## 运行样例

```bash
# 生成计划（JSON 输出到 stdout）
python3.11 -m rebuildplanner plan examples/packages.json examples/build.json

# 写入文件
python3.11 -m rebuildplanner plan examples/packages.json examples/build.json -o examples/plan.json

# 逐节点验证计划
python3.11 -m rebuildplanner verify examples/packages.json examples/build.json examples/plan.json
```

随仓库示例（`examples/`）的最优计划：

| 节点 | 动作 | 成本 | 原因 |
|---|---|---|---|
| `util-1.0.0` | reuse | 0 | 产物有效 |
| `core-2.0.0` | rebuild | 100 | 失效且无缓存，必须重建 |
| `web-1.4.0` | rebuild | 60 | 依赖 core 重建，联动失效（自身缓存匹配也不能用） |
| `api-1.2.0` | rebuild | 50 | 依赖 core 重建，联动失效 |
| `cli-2.0.0` | restore | 15 | 失效；缓存摘要匹配，恢复 |
| `app-3.0.0` | rebuild | 40 | 失效；缓存摘要为旧值（不匹配），不可恢复 |

**总成本 265**。要点：`core` 被两个目标链路共享，100 只计一次；`app` 的缓存条目
摘要为 `sha256:OLD-stale-digest`，不匹配因此被忽略。

`verify` 对计划逐节点检查：动作是否合法、摘要是否一致、依赖被重建时是否联动重建、
缓存摘要是否匹配、逐项成本与 `total_cost` 是否一致、闭包是否完整且无多余节点、
以及成本是否为求解器认定的最小值。任一不满足输出 `INVALID` 并以非零码退出，例如
把无缓存的 `core` 伪造成 `restore` 会得到：

```
INVALID: 节点 core-2.0.0 缓存缺失或摘要不匹配，不可恢复
```

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

覆盖：SemVer 解析/排序/预发布、`^`/`~`/比较运算/预发布门控、共享中间产物只计一次、
目标闭包裁剪、缓存匹配恢复、摘要不匹配不可恢复、恢复与重建连锁的最优权衡、
校验器对各类非法计划的拒绝、输入模型校验（未知包、缺失版本、约束冲突、环等）。
