# buildsys — 条件依赖最小构建集合规划器

纯 Python 3.11 标准库实现（测试用 `unittest`），不联网。从本地 JSON 读取包索引与构建输入，
按**目标 OS / 架构 / 阶段**条件求值依赖边，生成**最小实际构建集合**；宿主工具与目标产物
**分离缓存**，两个目标可共享同一宿主工具；每条依赖边的条件求值过程都输出为**证据**。

## 目录结构

```
buildsys/
  version.py    # major.minor.patch[-prerelease] 版本与约束（>=、<、==、!=、*）
  model.py      # 条件/依赖/包索引/构建输入的数据模型与 JSON 加载
  resolver.py   # 条件求值、版本选择、冲突检测、最小构建集合、证据
  cache.py      # 宿主/目标分离的缓存目录布局
  cli.py        # 命令行入口（python3 -m buildsys plan ...）
tests/          # unittest：28 个用例
examples/       # 示例包索引与构建输入
```

## 输入 JSON

包索引（`examples/packages.json`）：每个包版本可带依赖边，边上可标
`kind`（`host` 宿主工具 / `target` 目标产物，缺省继承父级域）与
`when`（`os` / `arch` / `stage` 允许列表，缺省维度恒真）：

```json
{"name": "app", "version": "1.0.0", "deps": [
  {"name": "codegen", "constraint": ">=2.0.0 <3.0.0", "kind": "host"},
  {"name": "posixsock", "constraint": "==3.1.0", "when": {"os": ["linux"]}},
  {"name": "testharness", "constraint": ">=1.0.0", "when": {"stage": ["test"]}}
]}
```

构建输入（`examples/build-linux-x86_64.json`）：

```json
{"target": {"os": "linux", "arch": "x86_64"}, "stage": "build",
 "host": {"os": "macos", "arch": "arm64"},
 "roots": [{"name": "app", "constraint": "==1.0.0"}]}
```

版本为 `major.minor.patch`，可带预发布（如 `2.0.0-rc.1`，预发布排在同版正式版之前，
数字标识符小于字母标识符）。

## 语义要点

- **最小构建集合**：只有条件为真的边才纳入图，假条件边的整棵子树（哪怕包不存在于索引中）
  都不会被查找、不参与版本交集，因此**假条件依赖不参与冲突**。
- **交叉编译**：`kind: "host"` 的边进入宿主域（生成器按宿主运行），其依赖继续继承宿主域；
  目标库按目标编译。两个目标解析出的宿主工具集合完全一致。
- **分离缓存**：`cache/host-<os>-<arch>/<name>/<version>/` 与
  `cache/target-<os>-<arch>/<name>/<version>/`，同一宿主上多个目标共享宿主工具缓存，
  重复构建幂等（`created` / `cached`）。
- **冲突检测**：同一（域, 包）上所有真条件约束取交集，无可用版本即报
  `ConflictError` 并列出每条约束的来源。
- **证据**：每条被求值的边（含被排除的）都记录条件、逐维度 `required/actual/matched`、
  是否纳入及原因，写入 `--evidence` 指定的 JSON。

## 运行样例

```
$ python3 -m buildsys plan --packages examples/packages.json \
    --input examples/build-linux-x86_64.json \
    --input examples/build-windows-x86_64.json \
    --input examples/build-linux-test.json \
    --cache-dir out/cache --evidence out/evidence.json
== target linux-x86_64 (stage=build) ==
minimal build set (6 packages):
  [host  ] libtempl 1.4.2
  [target] fontlib 1.0.0
  [target] posixsock 3.1.0
  [host  ] codegen 2.1.0
  [target] guilib 1.5.0
  [target] app 1.0.0
cache: 6 created, 0 reused
condition evidence: 9 edges evaluated, 4 excluded by false conditions

== target windows-x86_64 (stage=build) ==
minimal build set (6 packages):
  [host  ] libtempl 1.4.2
  [target] fontlib 1.0.0
  [target] win32sock 3.1.0
  [host  ] codegen 2.1.0
  [target] guilib 1.5.0
  [target] app 1.0.0
cache: 4 created, 2 reused        # codegen/libtempl 命中共享宿主缓存
condition evidence: 9 edges evaluated, 4 excluded by false conditions

== target linux-x86_64 (stage=test) ==
minimal build set (7 packages):   # test 阶段多入 testharness
  [host  ] libtempl 1.4.2
  [target] fontlib 1.0.0
  [target] posixsock 3.1.0
  [target] testharness 1.2.0
  [host  ] codegen 2.1.0
  [target] guilib 1.5.0
  [target] app 1.0.0
cache: 1 created, 6 reused
condition evidence: 9 edges evaluated, 3 excluded by false conditions

evidence written to out/evidence.json
```

缓存目录（宿主工具与目标产物分离，两个目标共享 `host-macos-arm64`）：

```
out/cache/host-macos-arm64/{codegen,libtempl}/...
out/cache/target-linux-x86_64/{app,fontlib,guilib,posixsock,testharness}/...
out/cache/target-windows-x86_64/{app,fontlib,guilib,win32sock}/...
```

证据节选（`out/evidence.json`，linux 目标上 `win32sock` 边被排除）：

```json
{"parent": "app-1.0.0", "package": "win32sock", "constraint": "==3.1.0",
 "domain": "target", "condition": {"os": ["windows"]},
 "evaluation": [
   {"dimension": "os", "required": ["windows"], "actual": "linux", "matched": false},
   {"dimension": "arch", "required": null, "actual": "x86_64", "matched": true},
   {"dimension": "stage", "required": null, "actual": "build", "matched": true}],
 "included": false, "reason": "condition false on: os"}
```

真条件冲突会失败并说明来源（假条件的 `legacy ==0.0.1` 不会触发任何错误）：

```
$ python3 -m buildsys plan --packages examples/packages.json \
    --input examples/build-conflict.json --cache-dir out/cache2 --evidence out/e2.json
error: conflict for target/guilib: no known version satisfies all requirements:
  ==9.9.9 (required by <root>)
  >=1.2.0 (required by app-1.0.0)
  available: 1.5.0
```

## 测试结果

```
$ python3 -m unittest discover -s tests -v
...
Ran 28 tests in 0.019s

OK
```

覆盖：版本解析/预发布排序（semver.org 顺序链）、约束匹配、OS/架构/阶段条件、
最小集合排除假条件子树、假条件依赖不参与冲突、真条件冲突报错、宿主/目标域传播、
两目标共享宿主工具、缓存分离与幂等、构建顺序拓扑、循环检测。
