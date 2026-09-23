# lockmerge — 三方锁文件合并工具

纯离线、仅标准库的 Python 3.11 三方锁文件合并器。包与构建输入全部来自本地
JSON 注册表，不联网查包；版本号为 `major.minor.patch`，可带预发布标记
（如 `1.5.0-rc1`）。

## 输入格式

锁文件（`base` / `ours` / `theirs` 同构）：

```json
{
  "roots": {"app": "1.0.0"},
  "build_inputs": {"python": ">=3.11,<4"},
  "packages": {"app": {"version": "1.0.0", "integrity": "sha256:..."}}
}
```

本地包注册表（闭包验证的唯一事实来源）：

```json
{"packages": {"web": {"2.4.0": {"dependencies": {"util": "^1.2.0"}}}}}
```

## 合并规则

- 独立包变更（单边新增/升级/删除）自动合并，并记录来源
  （`ours` / `theirs` / `both` / `base`）。
- 双方对同一包做相同升级时只保留一次（来源记为 `both`）。
- 双方对共享包/构建输入做了不同变更时**不直接拼接**：保留 base 值并记为
  冲突块（`both_changed_conflict` / `both_added_conflict` /
  `modify_delete_conflict`），合并后对闭包重新验证。
- 删除仍被引用的包（被保留包依赖或仍是 root）一律拒绝，恢复 base 版本并
  记入 `rejected_deletions`。
- 闭包验证全部基于本地注册表：未知版本、缺失依赖、约束违例、root 与锁定
  版本不一致都会列入 `closure_violations`；孤儿包仅警告。
- 存在冲突块、被拒绝的删除或闭包违例时，整体状态为 `needs_manual`，
  全部不可自动处理项汇总在 `unprocessable` 中，CLI 退出码为 2。

## 用法

```bash
PYTHONPATH=. python3.11 -m lockmerge \
  --base examples/base.lock.json \
  --ours examples/ours.clean.lock.json \
  --theirs examples/theirs.clean.lock.json \
  --registry examples/registry.json \
  --out merged.json
```

## 运行样例

`examples/` 下有三套样例，输出已生成：

| 场景 | 输入 | 结果 | 退出码 |
| --- | --- | --- | --- |
| 独立变更自动合并 | `ours.clean` / `theirs.clean` | `merged.clean.json`，`status=merged` | 0 |
| 共享依赖冲突 | `ours.conflict` / `theirs.conflict` | `merged.conflict.json`，`status=needs_manual` | 2 |
| 删除仍被引用 | `ours.delete` / `theirs.delete` | `merged.delete.json`，`status=needs_manual` | 2 |

冲突场景中：ours 把 `web` 升到 2.5.0，theirs 新增 root `worker@3.0.0`
（要求 `web ^3.0.0`）并把共享包 `util` 升到 2.0.0。合并器不拼接两个 `web`
变更，保留 base 的 2.4.0 并记冲突；闭包重新验证发现 `worker@3.0.0` 需要
`web ^3.0.0`、`app@1.1.0`/`web@2.4.0` 需要 `util ^1.x` 与锁定的 2.0.0 冲突，
全部列入 `unprocessable`。

## 测试

```bash
PYTHONPATH=. python3.11 -m unittest discover -s tests -v
```

37 个用例覆盖：版本解析与预发布排序、约束匹配（`>=`/`^`/`~`/组合）、
独立变更自动合并、相同升级去重、共享冲突不拼接、删除拒绝、闭包验证与
CLI 端到端。最近一次运行结果见 `test-results.txt`。
