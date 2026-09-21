# 构建事件状态归集（离线，纯标准库）

从一个**可能乱序、重复、迟到**的构建事件流中重建每个节点的状态。
包清单与构建定义全部来自**本地 JSON**，不联网查询任何包。

- Python 3.11（只用标准库），测试使用 `unittest`
- 终态判定只基于完整证据，证据不足一律输出为**未决（PENDING）**，绝不猜测成功

## 事件模型

| 事件 kind | 必需字段 | 含义 |
| --- | --- | --- |
| `node.started` | `id,node,attempt,ts` | attempt 开始 |
| `artifact.written` | + `artifact`（或 `artifacts` 列表） | 产物写入 |
| `artifact.committed` | + `artifact`（或 `artifacts` 列表） | 产物提交到输出存储 |
| `node.succeeded` | 基础字段 | worker 报告成功 |
| `node.failed` | 基础字段（可带 `reason`） | worker 报告失败 |

## 状态规则（核心）

- **SUCCESS**：`started` + `succeeded` + 每个必需产物都同时有 `written` 和
  `committed` 证据。收到成功事件但缺任一产物证据 → **PENDING**，并列出缺口。
- **FAILED**：收到该 attempt 的 `node.failed`。
- **PENDING**：其他一切（无事件、仅开始、证据不全……）。`missing` 字段说明缺什么。

乱序与重复：
- 事件按证据折叠，处理顺序不影响结果（纯函数式折叠）。
- 同一 `id` 重复出现是**幂等 no-op**；相同 id 但内容矛盾会报错。
- 每个节点以**最大 attempt 号**为权威：旧 attempt 的迟到失败**不能覆盖**新
  attempt 的成功；新 attempt 仍在进行时节点为 PENDING。

## 版本规则（SemVer，离线）

`major.minor.patch[-prerelease][+build]`，可标预发布。约束支持：精确版本、
`*`/`latest`、比较器集合（`>=1.2.0 <2.0.0`）、caret/tilde（`^1.2.0`、
`~1.2.3`）。正式版优先级高于同核心版本的预发布（`1.0.0 > 1.0.0-rc.1`）；
预发布标识按 SemVer 2.0 比较（数字按数值、数字低于字母、前缀短者更低）。

## 输入文件

- `sample_data/packages.json` — 本地包目录（包 → 可用版本列表）
- `sample_data/build_input.json` — 节点、包约束、`required_artifacts`、`depends_on`
- `sample_data/events.log.json` — 事件数组（样例刻意乱序并含 2 个重复事件、
  1 个旧 attempt 的迟到失败）

## 运行

```bash
python3.11 -m ci_state \
  --packages sample_data/packages.json \
  --input sample_data/build_input.json \
  --events sample_data/events.log.json
# 加 --json 输出机器可读报告
```

退出码：`0` 全部成功；`1` 存在 FAILED；`2` 存在 PENDING（证据不足）。

### 样例结果

```
Resolved packages (offline catalog):
  - lib: libcore==1.3.0 | depends_on: -
  - api: libcore==1.3.0-rc.1, netkit==1.0.0 | depends_on: lib
  - worker: netkit==1.0.0 | depends_on: lib
  - gateway: libcore==1.3.0 | depends_on: api
  - scheduler: (none) | depends_on: worker
  - docs: (none) | depends_on: -
  - nightly: netkit==0.9.1 | depends_on: -

[SUCCESS  (evidence complete)] node=api ... attempt=2 observed_attempts=[1, 2]
[PENDING  (undecided - not guessed)] node=docs ... no events observed
[SUCCESS  (evidence complete)] node=gateway ...   # 事件乱序到达
[SUCCESS  (evidence complete)] node=lib ...       # 重复事件被忽略
[PENDING] node=scheduler ... attempt=2 仅 started（旧 attempt1 已失败）
[PENDING] node=worker ... 产物已写未提交
[FAILED   (integration test red)] node=nightly

Summary: 3 success, 1 failed, 3 pending (of 7 nodes); 2 duplicate event(s) ignored
Undecided nodes (need more evidence): docs, scheduler, worker
```

关键演示点：
- `api` attempt 1 失败，attempt 2 证据完整；一条 attempt 1 的迟到失败（ts=400）
  到达后状态仍是 SUCCESS。
- `worker` 只有 `artifact.written` 没有 `artifact.committed`，即使有成功事件
  也保持 PENDING。
- `docs` 没有任何事件，输出为未决而不是猜测成功。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

覆盖：SemVer 解析/预发布排序、证据完备性（缺写/缺提交/缺成功事件/多产物缺一）、
乱序折叠一致性、重复幂等、同 id 矛盾报错、旧失败不覆盖新成功、新失败覆盖旧成功、
新 attempt 进行中为 PENDING、离线包解析、CLI 退出码与 JSON 报告。

## 代码结构

- `ci_state/version.py` — SemVer 解析与比较
- `ci_state/events.py` — 事件 dataclass 与校验
- `ci_state/engine.py` — 证据折叠、attempt 权威选择、节点报告
- `ci_state/loader.py` — 本地 JSON 加载、`PackageCatalog` 离线版本解析
- `ci_state/main.py` — 命令行（文本 / `--json`）
- `tests/` — unittest 套件
