# stageplan — 本地分阶段构建规划器

仅使用 Python 3.11 标准库（`dataclasses`、`json`、`argparse`、`unittest` 等），
**不联网查询任何包**。包与构建输入全部来自本地 JSON 文件。

## 要解决的问题

- 普通包依赖（`depends_on`）**允许成环**：同一个强连通分量（SCC）作为
  **一个共同编译组（co-compile group）**，组成员之间不伪造拓扑顺序。
- “生成器 → 产物 → 需要产物的包”是**硬阶段边**：
  `A produces X`、`B needs_artifacts [X]` 得到 `A → B`，A 必须在更早阶段。
- 一旦某条环包含硬边（生成器依赖了自己产物链条上的东西，包括自己产自用），
  这是**禁止的跨阶段环**，规划被拒绝，并给出具体环边证据。
- 被阻塞组的下游一律不可构建；与阻塞无关的组仍产出正常可执行计划。

## 输入 JSON

```json
{
  "packages": [
    {
      "name": "core",
      "version": "1.4.2",
      "depends_on": ["other", {"name": "lib", "version": "^1.2.0"}],
      "produces": ["libcore.a"],
      "needs_artifacts": ["codegen.h"]
    }
  ]
}
```

- `version`：`major.minor.patch`，可带预发布后缀（如 `2.0.0-rc.1`）与
  构建元数据（`+build`，比较时忽略）。
- `depends_on`：字符串表示任意版本；对象形式用 `version` 给约束。
  支持 `== != >= <= > <`、空格分隔的 AND 区间、`^`（兼容范围）、`~`（同 minor）。
  稳定版区间不会静默接受预发布版本；要接受预发布必须在约束中显式命名它。
- `produces` / `needs_artifacts`：产物名。唯一生产者生成硬边；
  重复生产者、无生产者、自己产自用分别构成具体阻塞证据。

## 阶段如何计算

1. 以“前置 → 消费者”方向同时建立软边（依赖）和硬边（产物）。
2. 在全部边上跑 SCC（迭代式 Kosaraju，避免深递归）。
3. 内部含硬边的 SCC 直接标记 `CROSS_STAGE_CYCLE`，并用 BFS 取一条
   优先经过硬边的具体闭环作为证据；自环同样给出 `A --hard--> A`。
4. 版本不匹配、未知依赖/产物、重复产物等记为 `DEPENDENCY_BLOCKED`。
5. 在 SCC 缩点 DAG 上，从每个无效组沿下游传播 `UPSTREAM_BLOCKED`，
   并保留一条具体的边链说明“为什么被谁阻塞”。
6. 对剩余可执行 SCC，用缩点 DAG 上的最长路径确定阶段
   （`stage = 1 + max(前置阶段)`）；互不依赖的组自然落在同一阶段。
7. `--target` 只取目标沿前置方向的闭包；范围外的阻塞不影响目标结论。

退出码：`0` 计划可行；`1` 存在阻塞（但仍打印证据和可执行部分）；
`2` 输入 JSON 结构性错误。

## 运行样例

```bash
python3.11 -m stageplan examples/packages.json
python3.11 -m stageplan examples/packages.json --target server
python3.11 -m stageplan examples/blocked.json            # 含跨阶段环，退出码 1
python3.11 -m stageplan examples/blocked.json --json
```

`examples/packages.json` 的阶段结果：

- Stage 1：`core`；以及共同编译组 `{app-config, app-util}`（软环，不排内部顺序）
- Stage 2：`json`、`net`（互不依赖，同阶段）
- Stage 3：`server`

`examples/blocked.json` 中：

- `gen-a/gen-b/gen-c` 构成硬边跨阶段环，输出具体环：
  `gen-a --hard:artifact:art-a--> gen-c --hard...--> gen-b --hard...--> gen-a`；
- `consumer` 因依赖 `art-a` 被标记 `UPSTREAM_BLOCKED` 并给出边链；
- `self-gen` 产自用形成硬自环；`badver` 版本约束不满足；
- 无关的 `good-base/good-app` 与软环 `ring-x/ring-y` 仍给出正常计划。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

覆盖：版本/预发布排序与比较、`^`/`~` 与显式预发布门槛、软环成组不排序、
硬边严格分阶段、同阶段并行、跨阶段环与硬自环拒绝及证据、下游隔离而无关组
可执行、未知依赖/产物与版本不匹配、目标闭包、畸形包定义剔除等。
