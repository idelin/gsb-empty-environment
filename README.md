# compatcheck

离线 API 兼容性影响分析工具（Python 3.11，仅标准库）。输入为本地 JSON：
两版本导出符号快照（名称、可见性、参数类型、返回类型、行为标记）、类型层级、
版本号（`major.minor.patch`，可带预发布号）以及依赖调用图。工具计算每个公开
符号的兼容/破坏判定，并沿调用边把破坏变更反向传播到根入口，输出具体破坏路径
与受影响调用点。全程不联网。

## 判定规则

- **输入位置（参数，逆变）**：类型扩大兼容，收窄或变为无关类型破坏。
- **输出位置（返回值，协变）**：类型收窄兼容，扩大或变为无关类型破坏。
- **行为标记**：标记是承诺，移除即破坏，新增为兼容增强。
- **删除符号**：仅当存在从根到该符号的调用链时才影响该根；未被调用的删除
  不影响根（列为"不可达"）。
- **私有符号**：变动不计入公开破坏，单独列为已忽略。
- **版本校验**：公开破坏要求 major 递增（0.x 包按 semver 第 4 条接受 minor），
  兼容新增要求 minor；预发布会给出提示。

## 输入格式

见 `examples/breaking.json`。要点：

- `type_hierarchy`: 类型 -> 直接父类型列表（传递闭包由工具计算）。
- `old`/`new.symbols[]`: `name`、`visibility`（`public`/`private`）、
  `params[]`（`name`/`type`/`optional`）、`returns`、`behaviors[]`。
- `callgraph`: `roots` 与 `edges`（`caller`/`callee`/可选 `site`）。

## 运行

```bash
python3 -m compatcheck examples/breaking.json          # 文本报告
python3 -m compatcheck examples/breaking.json --json   # JSON 报告
```

退出码：有根受破坏影响为 1，否则 0，输入非法为 2。

## 运行样例

`python3 -m compatcheck examples/breaking.json`（退出码 1）：

```
Package: textkit 1.4.2 -> 2.0.0-rc.1 (pre-release)
Version bump: major (required: major) -- OK
  note: 2.0.0-rc.1 is a pre-release; breakage may still land before the stable release

Package impact: BREAKING
Public API changes:
  [BREAKING ] textkit.legacy_format: symbol removed
  [BREAKING ] textkit.parse: return type widened: dict -> object (output position: breaking)
  [ok       ] textkit.slugify: parameter 'text' type widened: str -> object (input position: compatible widening)
  [BREAKING ] textkit.tokenize: behavior guarantee 'thread-safe' removed
  [BREAKING ] textkit.trim: parameter 'text' type unrelated: str -> bytes (input position: breaking)
  [ok       ] textkit.word_count: symbol added

Root impact: BREAKING
Affected roots: app.main
Breaking paths:
  app.main -> app.service.handle -> textkit.parse
    change: return type widened: dict -> object (output position: breaking)
    call sites: app/main.py:12, app/service.py:34
  app.main -> app.worker.run -> textkit.tokenize
    change: behavior guarantee 'thread-safe' removed
    call sites: app/main.py:20, app/worker.py:8
Affected call sites: app/main.py:12, app/main.py:20, app/service.py:34, app/worker.py:8

Breaking changes not reachable from any root (no impact):
  textkit.legacy_format: symbol removed
  textkit.trim: parameter 'text' type unrelated: str -> bytes (input position: breaking)

Ignored private-symbol changes (not public breakage):
  textkit._cache_clear: parameter 'force' (int) added
```

`python3 -m compatcheck examples/compatible.json`（退出码 0）：

```
Package: textkit 1.4.2 -> 1.5.0
Version bump: minor (required: minor) -- OK

Package impact: COMPATIBLE
Public API changes:
  [ok       ] textkit.parse: parameter 'strict' (int) added as optional
  [ok       ] textkit.parse: behavior guarantee 'deterministic' added
  [ok       ] textkit.slugify: parameter 'text' type widened: str -> object (input position: compatible widening)
  [ok       ] textkit.word_count: symbol added

Root impact: NONE
No root is affected by breaking changes.
```

## 测试

```bash
python3 -m unittest discover -s tests -v
```

测试结果（29 个用例全部通过）：

```
test_breaking_example (test_compatcheck.TestAnalyze.test_breaking_example) ... ok
test_compatible_example (test_compatcheck.TestAnalyze.test_compatible_example) ... ok
test_required_bump_levels (test_compatcheck.TestAnalyze.test_required_bump_levels) ... ok
test_version_violation_when_breaking_without_major (test_compatcheck.TestAnalyze.test_version_violation_when_breaking_without_major) ... ok
test_zero_major_accepts_minor_for_breaking (test_compatcheck.TestAnalyze.test_zero_major_accepts_minor_for_breaking) ... ok
test_breaking_exit_code_and_output (test_compatcheck.TestCli.test_breaking_exit_code_and_output) ... ok
test_compatible_exit_code (test_compatcheck.TestCli.test_compatible_exit_code) ... ok
test_invalid_input_exit_code (test_compatcheck.TestCli.test_invalid_input_exit_code) ... ok
test_json_output (test_compatcheck.TestCli.test_json_output) ... ok
test_added_optional_param_compatible_required_breaking (test_compatcheck.TestDiffSymbols.test_added_optional_param_compatible_required_breaking) ... ok
test_behavior_removed_breaking_added_compatible (test_compatcheck.TestDiffSymbols.test_behavior_removed_breaking_added_compatible) ... ok
test_made_private_counts_as_removal (test_compatcheck.TestDiffSymbols.test_made_private_counts_as_removal) ... ok
test_param_narrowing_is_breaking (test_compatcheck.TestDiffSymbols.test_param_narrowing_is_breaking) ... ok
test_param_unrelated_is_breaking (test_compatcheck.TestDiffSymbols.test_param_unrelated_is_breaking) ... ok
test_param_widening_is_compatible (test_compatcheck.TestDiffSymbols.test_param_widening_is_compatible) ... ok
test_private_changes_are_not_public (test_compatcheck.TestDiffSymbols.test_private_changes_are_not_public) ... ok
test_removed_param_is_breaking (test_compatcheck.TestDiffSymbols.test_removed_param_is_breaking) ... ok
test_removed_symbol_is_breaking_change (test_compatcheck.TestDiffSymbols.test_removed_symbol_is_breaking_change) ... ok
test_return_narrowing_is_compatible (test_compatcheck.TestDiffSymbols.test_return_narrowing_is_compatible) ... ok
test_return_widening_is_breaking (test_compatcheck.TestDiffSymbols.test_return_widening_is_breaking) ... ok
test_cycle_does_not_hang (test_compatcheck.TestPropagate.test_cycle_does_not_hang) ... ok
test_path_and_sites (test_compatcheck.TestPropagate.test_path_and_sites) ... ok
test_uncalled_symbol_reaches_no_root (test_compatcheck.TestPropagate.test_uncalled_symbol_reaches_no_root) ... ok
test_relations (test_compatcheck.TestTypeEnv.test_relations) ... ok
test_bump_level (test_compatcheck.TestVersion.test_bump_level) ... ok
test_parse_invalid (test_compatcheck.TestVersion.test_parse_invalid) ... ok
test_parse_plain (test_compatcheck.TestVersion.test_parse_plain) ... ok
test_parse_prerelease (test_compatcheck.TestVersion.test_parse_prerelease) ... ok
test_prerelease_ordering (test_compatcheck.TestVersion.test_prerelease_ordering) ... ok

----------------------------------------------------------------------
Ran 29 tests in 0.960s

OK
```

## 代码结构

- `compatcheck/semver.py` — semver 解析、预发布排序、bump 分级。
- `compatcheck/core.py` — 类型层级、符号 diff（输入/输出位型变规则）、
  沿调用边反向传播到根、版本校验、顶层 `analyze`。
- `compatcheck/report.py` — 文本报告渲染。
- `compatcheck/__main__.py` — CLI 入口。
- `examples/` — 破坏与兼容两个样例输入。
- `tests/test_compatcheck.py` — unittest 套件。
