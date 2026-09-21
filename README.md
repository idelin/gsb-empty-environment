# 构建状态重建（event-sourced build state）

从本地 JSON 构建定义和 JSONL 事件日志重建每个构建节点的状态。纯 Python 3.11
标准库实现，**不联网查询任何包信息**；包版本只来自本地构建定义。

## 设计语义

- 事件按 `(node, attempt)` 收集，`attempt` 从 1 开始，数字越大越是新的一次重试。
- 事件可以**乱序到达、重复到达**：状态由完整事件集合推导，与到达顺序无关；
  以 `event_id` 去重，同一 `event_id` 重复且内容相同为幂等无操作；内容冲突
  则保留第一次并产生告警。
- 单个 attempt 的判定：
  - 有 `failed` 事件 → `failed`（终态，记录原因）。
  - 同时存在 `submitted` 与 `failed` → 证据矛盾，`pending`，不猜测。
  - 有 `submitted` 但缺少 `started`、缺少任一必需产物证据，或同一产物出现
    多个不同 sha256 → 一律 `pending`，并列出缺失/冲突证据。
  - `started` + 全部必需产物（sha256 一致）+ `submitted` 证据齐全 →
    `success`。
- 节点状态由**最大 attempt 号**决定：旧 attempt 的失败（即使延迟、乱序到达）
  不能覆盖新 attempt 的成功；新 attempt 仍在进行时，节点为 `pending`。
- 没有任何事件的节点是 `pending`（未决），输出到 UNRESOLVED 列表，绝不猜测
  成功。

## 事件格式（JSONL，每行一个事件）

```json
{"event_id": "evt-1", "timestamp": "2026-09-21T10:00:00Z", "node": "compile-core",
 "attempt": 1, "kind": "started"}
{"event_id": "evt-2", "timestamp": "...", "node": "compile-core", "attempt": 1,
 "kind": "artifacts_written",
 "artifacts": [{"name": "core.o", "sha256": "<64位hex>"}]}
{"event_id": "evt-3", "timestamp": "...", "node": "compile-core",
 "attempt": 1, "kind": "submitted"}
{"event_id": "evt-4", "timestamp": "...", "node": "compile-core",
 "attempt": 1, "kind": "failed", "reason": "compile error"}
```

`kind` ∈ `started | artifacts_written | submitted | failed`。
`failed` 必须带非空 `reason`；产物 sha256 必须是 64 位十六进制。

## 构建定义格式（本地 JSON）

- `packages`：包名 → 精确版本。版本为 `major.minor.patch`，可选连字符
  预发布（如 `0.9.0-rc.2`、`1.0.0-beta.1`），遵循 SemVer 优先级
  （预发布低于正式版，数字标识按数值比较）。
- `nodes`：节点 → `inputs`（必须引用已声明包且版本逐位一致）与
  `requires_artifacts`（成功所需的全部产物名，至少一个）。

## 运行

```bash
python3 -m buildstate.cli \
  --definition examples/build.json \
  --events examples/events.jsonl
# 或
./run_examples.sh
```

退出码：`0` 全部成功；`1` 全部终态但有失败；`2` 存在未决节点；`64` 输入
（定义或事件）非法。

## 样例覆盖的场景

`examples/build.json` + `examples/events.jsonl` 包含：

- `compile-core`：证据完整的成功（事件故意乱序）。
- `package-net`：attempt 1 失败；attempt 2 已 submitted 但缺少
  `net.tar.gz.sha256` 证据 → 未决；attempt 1 的失败延迟乱序到达，不能
  覆盖 attempt 2；submitted 事件重复提交（幂等）。
- `test-flaky`：attempt 1 失败，attempt 2 成功 → 节点成功，旧失败不覆盖。
- `ship-bridge`：没有任何事件 → 未决。
- `ghost-node`：未知节点事件 → 告警并忽略。

样例报告见 `examples/sample_output.txt`（退出码 2，因为存在未决节点）。

## 测试

```bash
python3 -m unittest discover -v
```

41 个 unittest 用例，覆盖版本解析/优先级、定义校验、事件解析、乱序与重复
幂等、缺证据未决、产物哈希冲突、终态矛盾、旧失败不覆盖新成功、延迟失败、
未知节点告警，以及 CLI 退出码与 JSONL 行号错误报告。

## 代码结构

- `buildstate/version.py`：版本解析与比较（纯本地，无网络）。
- `buildstate/model.py`：构建定义加载与校验。
- `buildstate/events.py`：事件模型、严格校验与 JSONL 读取。
- `buildstate/tracker.py`：事件收集、去重与状态推导核心。
- `buildstate/cli.py`：命令行入口与报告渲染。
- `tests/`：unittest 测试套件。
