# 版本化时间段授权（authz）

纯 Python 3.11+ 标准库实现，无第三方依赖。所有示例数据均为合成数据。

## 核心语义

- **规则**：`allow` / `deny`，字段为主体（`subject`）、资源前缀（`prefix`）、
  操作（`action`）、有效区间 `[start, end)`（`null` 表示无界）。
  `subject` / `action` 支持 `"*"` 通配。
- **拒绝优先**：任一匹配的 deny 规则胜出；无匹配 allow 时默认拒绝。
- **路径规范化**：折叠重复分隔符、解析 `.` / `..`，`..` 在根处被截断，
  无法越过根（`/root/a/../private/x` → `/root/private/x`，命中拒绝规则）。
- **前缀匹配按段对齐**：`/root/private` 不匹配 `/root/privatex`。
- **审计**：每次评估返回固定策略版本号的审计记录。
- **版本差异**：给定有限资源集合（及可选主体/操作集合），求两版本决策
  最早分歧时间及见证请求。决策只在规则区间边界变化，因此探测所有边界
  点（外加一个先于所有边界的哨兵点）即精确。

## 策略文件格式（JSON 输入）

```json
{
  "version": "v1",
  "rules": [
    {"id": "allow-root-read", "effect": "allow", "subject": "*", "action": "read",
     "prefix": "/root", "start": null, "end": null},
    {"id": "deny-private-read", "effect": "deny", "subject": "*", "action": "read",
     "prefix": "/root/private", "start": "2026-01-01T00:00:00Z", "end": null}
  ]
}
```

- `version`（必填）：版本标识，会固定到每条审计记录。
- `effect`（必填）：`allow` 或 `deny`；`prefix`（必填）：资源前缀。
- `subject` / `action`：缺省为 `"*"`。
- `start` / `end`：ISO-8601 时间（支持 `Z` 后缀；无时区按 UTC），`null` 表示无界。

## 命令行

### `check`：评估单个请求

```sh
python3 authz.py check --policy examples/policy_v1.json \
  --subject alice --resource /root/a/../private/x --action read \
  --time 2026-03-01T00:00:00Z
```

输出（JSON，退出码：允许=0，拒绝=1，错误=2）：

```json
{
  "version": "v1",
  "decision": "deny",
  "subject": "alice",
  "action": "read",
  "resource": "/root/private/x",
  "time": "2026-03-01T00:00:00+00:00",
  "matched_allow": ["allow-root-read"],
  "matched_deny": ["deny-private-read"],
  "reason": "matched deny rule (deny overrides)"
}
```

### `diff`：两版本最早权限差异

```sh
python3 authz.py diff --policy-a examples/policy_v1.json \
  --policy-b examples/policy_v2.json --resources /root/private/x /root/public/y \
  [--subjects alice bob] [--actions read write]
```

输出（退出码：发现差异=0，无差异=1，错误=2）：

```json
{
  "found": true,
  "time": "0001-01-01T00:00:00+00:00",
  "request": {"subject": "*", "action": "read",
              "resource": "/root/private/x", "time": "0001-01-01T00:00:00+00:00"},
  "version_a": "v1", "decision_a": "deny",
  "version_b": "v2", "decision_b": "allow"
}
```

`time` 为 `0001-01-01T00:00:00+00:00` 时是哨兵值，表示“所有规则边界之前
的任意时刻”（差异从时间起点就存在）。`--subjects` / `--actions` 缺省时
使用两版本规则中出现的全部主体/操作字面量。

## 测试

```sh
python3 -m unittest -v
```

32 个测试覆盖：路径规范化（点段、重复分隔符、根截断）、段对齐前缀匹配、
拒绝优先、时间区间边界、审计版本固定、跨版本最早差异与见证请求、CLI 端到端。

## 文件

- `authz.py` — 核心引擎 + CLI（`check` / `diff`）
- `test_authz.py` — unittest 测试
- `examples/policy_v1.json`、`examples/policy_v2.json` — 合成示例策略
