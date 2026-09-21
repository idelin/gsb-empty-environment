# lockmerge — 三方锁文件合并工具

对锁文件（lock file）做三方合并：独立包变更自动合并，共享依赖冲突时重新验证依赖闭包，
结果附带每项变更的来源（provenance）与无法自动处理的条目。所有包与构建输入均来自本地
JSON 文件，不联网查询包。Python 3.11，仅使用标准库，测试基于 unittest。

## 锁文件格式

```json
{
  "version": 1,
  "roots":        {"app": "^1.0.0"},
  "packages":     {"app": {"version": "1.0.0", "dependencies": {"lib": "^1.0.0"}}},
  "build_inputs": {"cflags": "-O2"}
}
```

- 版本号为 `major.minor.patch`，可带预发布后缀（如 `2.0.0-rc.1`），按 semver 2.0 排序。
- 约束支持 `>=` `<=` `>` `<` `==` `!=` `=`、`^`（兼容范围）、`~`（补丁范围）、
  裸版本（精确匹配）与 `*`（任意），多个比较子用空格或逗号连接。

## 合并规则

- 仅一侧修改的条目自动合并；两侧相同修改只保留一次；未修改条目保留 base。
- 同一包被两侧改成不同版本（或一侧删除、一侧修改）记为冲突，不直接拼接。
- 合并后对闭包重新验证：root 约束与每个包的依赖约束都必须被锁内版本满足。
  双方升级不同根导致共享包约束冲突、删除的包仍被引用、依赖缺失等情况都会
  列入 `unresolvable`，而不是产出损坏的锁文件。
- 输出中 `provenance` 标明每个条目来自 `base` / `ours` / `theirs` / `both`。

## 运行

```console
$ python3.11 -m lockmerge BASE OURS THEIRS [-o report.json]
```

退出码：`0` 合并干净；`1` 存在冲突或不可自动处理项；`2` 输入错误。

### 样例 1：独立变更自动合并

```console
$ python3.11 -m lockmerge examples/clean/base.json examples/clean/ours.json examples/clean/theirs.json
{
  "status": "clean",
  "lock": {
    "version": 1,
    "roots": {"app": "^1.1.0", "tool": "^2.2.0"},
    "packages": {
      "app":  {"version": "1.1.0", "dependencies": {"lib": "^1.3.0"}},
      "lib":  {"version": "1.3.1", "dependencies": {}},
      "tool": {"version": "2.2.0", "dependencies": {}}
    },
    "build_inputs": {"cflags": "-O3", "target": "aarch64"}
  },
  "provenance": {
    "roots": {"app": "ours", "tool": "theirs"},
    "packages": {"app": "ours", "lib": "ours", "tool": "theirs"},
    "build_inputs": {"cflags": "ours", "target": "theirs"}
  },
  "conflicts": [],
  "unresolvable": []
}
merge clean: all changes auto-merged        # 退出码 0
```

### 样例 2：双方升级不同根，共享包约束冲突

`ours` 把 `app-a` 升到 2.0.0（连带 `shared` 升到 2.0.0），`theirs` 把 `app-b`
升到 1.1.0（仍要求 `shared ^1.0.0`）。工具不会直接拼接，而是报出不可自动处理项：

```console
$ python3.11 -m lockmerge examples/conflict/base.json examples/conflict/ours.json examples/conflict/theirs.json
{
  "status": "needs-manual-resolution",
  "lock": { ... },
  "provenance": {
    "roots": {"app-a": "ours", "app-b": "theirs"},
    "packages": {"app-a": "ours", "app-b": "theirs", "shared": "ours"},
    "build_inputs": {}
  },
  "conflicts": [],
  "unresolvable": [
    {
      "kind": "constraint-violation",
      "package": "shared",
      "referenced_by": "packages:app-b",
      "constraint": "^1.0.0",
      "locked_version": "2.0.0",
      "reason": "merged lock has shared 2.0.0 which does not satisfy '^1.0.0' required by packages entry 'app-b'; closure must be re-resolved"
    }
  ]
}
merge needs manual resolution: 0 conflict(s), 1 unresolvable item(s)   # 退出码 1
```

## 测试

```console
$ python3.11 -m unittest discover -s tests -v
...
----------------------------------------------------------------------
Ran 29 tests in 0.003s

OK
```

覆盖：版本解析/排序（含预发布）、约束匹配（`^`/`~`/比较子）、独立变更自动合并、
相同升级去重、删除-修改冲突、删除仍被引用拒绝、共享依赖约束冲突、传递闭包验证、
构建输入合并、非法输入拒绝。

## 目录结构

```
lockmerge/
  versions.py   # 语义化版本与约束（纯标准库）
  lockfile.py   # 锁文件 JSON 加载与结构校验
  merge.py      # 三方合并 + 闭包重验证 + 来源标注
  cli.py        # 命令行入口（python -m lockmerge）
tests/          # unittest 测试
examples/       # 可运行样例（clean / conflict）
```
