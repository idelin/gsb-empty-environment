# 构建状态恢复（事件溯源，离线）

从本地 JSON 事件日志恢复每个构建节点的状态。仅使用 Python 3.11 标准库，
**不联网、不查询包仓库**。

## 语义约定

- **节点起点**：以构建输入中声明的节点为准；每个节点从 `submitted` 开始，
  没有任何事件的节点是 `PENDING`，并列出全部缺失证据。
- **终态成功需要完整证据**：`succeeded` 事件本身不够。必须同时具备
  - 每个 `required_artifacts` 都有一条产物证据，且产物的 `package/version`
    与构建输入精确匹配（sha256 只是证据标识）；
  - 当 `require_commit` 为真（默认）时，存在 `committed` 证据。
  
  收到 `succeeded` 但缺任何一项必需证据 → **PENDING（未决），绝不猜测成功**。
- **失败即终态**：`failed` 不要求产物证据，取同 attempt 内 seq 最大的失败原因。
  同 attempt 内 seq 更大的相反终态可以翻转（先失败后重试成功、或成功后回滚失败）。
- **attempt 隔离**：每个节点只由观测到的最新 attempt 决定状态；旧 attempt 的
  成功/失败/产物/提交都不能覆盖或泄漏到新 attempt。
- **乱序**：折叠前按 `(attempt, seq, event_id)` 确定性排序，结果与摄入顺序无关。
- **重复**：相同 `event_id` 幂等去重；同 attempt 不同 `event_id` 的重复提交
  不重置已有进度，只记录一条异常。
- **异常而非静默**：未声明节点（孤儿事件）、多余产物、包/版本不符、sha256
  冲突、commit 冲突、终态 seq 打平均记录在 `anomalies` 中，且都不会被当作证据。
- **版本**：`major.minor.patch[-prerelease]`（SemVer 风格），预发布可显式标记；
  同核心版本下 release > 预发布，预发布按标识符逐项比较。
- **包输入**：包版本只来自本地 `packages.json`；构建输入可精确钉版，或写
  `"latest"`（默认跳过预发布，节点声明 `include_prerelease: true` 才纳入）。

状态只有三种：`SUCCESS` / `FAILED` / `PENDING`。

## 运行

```sh
python3.11 -m build_state.main \
  --registry build_state/data/packages.json \
  --build-input build_state/data/build_input.json \
  --events build_state/data/events.json
```

加 `--strict` 时，只要存在 `PENDING` 节点，进程退出码为 1（CI 用）。

## 运行样例（随仓库数据的实测结果）

样例事件日志刻意打乱了顺序，包含重复 event_id、重复提交、旧 attempt 成功/失败、
产物版本不符、多余产物、sha256 冲突和未声明节点：

```
  node-a         SUCCESS attempt=1
  node-b         PENDING attempt=1 missing_artifacts=['symbols.tar'] missing_commit=True reason='succeeded event without complete required evidence'
  node-c         SUCCESS attempt=1
  node-d         FAILED  attempt=2 reason='flaky integration test'
  node-e         SUCCESS attempt=2
  node-f         SUCCESS attempt=1
  node-g         SUCCESS attempt=1

  success=5 failed=1 pending=1
  unresolved (reported, never guessed): ['node-b']
  duplicate event ids (idempotent): ['E023']
  orphan events (ignored): ['E090']
  anomaly[node-x]: event 'E090' references undeclared node 'node-x'; ignored
  anomaly[node-b]: event 'E013': artifact 'debug.map' is not in required artifacts [...]; not accepted as evidence
  anomaly[node-c]: event 'E024': duplicate submission for attempt 1 (already submitted by ['E020']); state kept
  anomaly[node-f]: event 'E074': artifact 'libcore-old.jar' sha256 'fff-OTHER' conflicts with previously written 'fff111'; keeping the first evidence
```

关键点：

- `node-b` 收到 `succeeded`，但缺 `symbols.tar` 且无 commit → 报未决并列出缺证。
- `node-d` attempt 1 证据完整且成功，attempt 2 失败 → 以新 attempt 失败为准。
- `node-e` attempt 1 的产物版本不符且声称成功；attempt 2 用正确版本重试成功，
  成功完全建立在 attempt 2 自己的证据上。
- 同一批事件反序摄入，节点状态完全一致（见端到端测试）。

## 测试

```sh
python3.11 -m unittest discover -s tests
```

实测：`Ran 49 tests ... OK`（版本解析与排序、离线包解析、证据/失败/attempt/
幂等/乱序/异常规则，以及基于样例 JSON 的端到端与 CLI 退出码测试）。

## 代码结构

- `build_state/versions.py`：版本解析与比较
- `build_state/registry.py`：离线包清单（精确钉版 / latest / 预发布开关）
- `build_state/models.py`：构建输入、事件、产物的数据类与本地 JSON 加载/校验
- `build_state/tracker.py`：去重、排序折叠、attempt 隔离、证据判定与报告
- `build_state/main.py`：CLI
- `build_state/data/`：本地包清单、构建输入、事件日志样例
- `tests/`：unittest 测试
