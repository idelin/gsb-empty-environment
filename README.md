# buildstate — 事件溯源的构建状态追踪器

从本地 JSON 读取包/构建输入与构建事件流，把**乱序、可重复**的事件归约为每个节点的构建状态。
终态必须证据完整；证据不足一律输出 `PENDING`，绝不猜测成功。全程不联网。

## 语义规则

- 节点 = `package@version`，版本严格为 `major.minor.patch`，可带 `-prerelease`（如 `0.9.0-rc.1`）。
- 事件类型：`node_started` / `artifact_written` / `committed` / `build_succeeded` / `build_failed`，均带 `attempt` 与唯一 `event_id`。
- 状态是**去重后事件集合**的纯函数：与到达顺序无关；重复 `event_id` 幂等忽略（计入 `duplicates_ignored`）。
- 节点只看**最大 attempt**：旧 attempt 的失败不能覆盖新 attempt 的成功；新 attempt 一旦开始，旧成功同样作废（保守）。
- `SUCCEEDED`（终态）要求同一 attempt 内证据完整：成功事件 + 全部必需产物 + 至少一次提交。
- 收到成功事件但缺任一必需产物（或缺提交）→ `PENDING`，并在 `missing` 中列出缺口。
- 同一 attempt 同时出现成功与失败属矛盾，保守判 `FAILED`。
- 未出现在 packages.json 中的节点事件被忽略并记入 `warnings`。

## 运行

```bash
python3 -m buildstate samples/packages.json samples/events.json
```

## 测试

```bash
python3 -m unittest discover -v
```

## 样例运行结果（摘要）

输入 `samples/events.json` 故意乱序并包含一个重复事件（`e007` 提交两次）：

| 节点 | 场景 | 结果 |
| --- | --- | --- |
| `alpha@1.2.0` | 乱序事件 + 重复提交，证据完整 | `SUCCEEDED`（`e007` 幂等忽略） |
| `beta@0.9.0-rc.1` | 有成功事件但缺一个必需产物 | `PENDING`，`missing: [dist/beta-0.9.0rc1-py3-none-any.whl]` |
| `gamma@2.0.0` | attempt 1 失败，attempt 2 成功且证据完整 | `SUCCEEDED`（旧失败不覆盖新成功） |
| `delta@1.0.1` | 最新 attempt 失败 | `FAILED` |
| `epsilon@3.1.4` | 仅收到 started | `PENDING` |

汇总：`{"total": 5, "succeeded": 2, "failed": 1, "pending": 2}`，`pending: [beta@0.9.0-rc.1, epsilon@3.1.4]`。

## 测试结果

```
Ran 24 tests in 0.027s
OK
```

覆盖：版本解析（含预发布与非法版本）、事件校验、完整证据成功、缺产物/缺提交未决、
旧 attempt 失败不覆盖新成功、最新 attempt 失败为终态、新 attempt 使旧成功作废、
同 attempt 矛盾判失败、重复事件幂等、乱序等价（20 组随机 shuffle）、
无事件/仅 started 未决、未知节点告警、报告列出未决节点、JSON 加载与错误处理。

## 结构

- `buildstate/version.py` — 版本解析与校验
- `buildstate/events.py` — 事件模型与校验
- `buildstate/nodes.py` — 节点规格
- `buildstate/loader.py` — 本地 JSON 加载
- `buildstate/reducer.py` — 事件归约与状态判定
- `buildstate/__main__.py` — CLI
- `tests/test_buildstate.py` — unittest 测试
- `samples/` — 示例输入
