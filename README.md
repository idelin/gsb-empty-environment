# depres — 锁文件感知的最小变更依赖解析器

Python 3.11 + 标准库（`dataclasses`、`argparse`、`json`、`re`、`unittest`），无第三方依赖、不联网。
所有包版本与构建约束均来自本地 JSON 文件。

## 文件与输入格式

- `depres.py` — 版本解析、约束匹配、分支限界求解器、因果链报告与 CLI（一个文件，可直接 `python3.11 depres.py ...`）
- `test_depres.py` — 21 个 `unittest` 用例
- `examples/packages.json` — 本地包索引：`{包名: {版本: {"deps": {依赖: 约束}}}}`
- `examples/build.json` — 构建输入（根约束）：`{"deps": {包名: 约束}}`
- `examples/lock.json` — 旧锁：`{包名: 版本}`
- `examples/build-no-server.json`、`examples/build-unsat.json`、`examples/lock-missing.json` — 演示用变体

版本为 `major.minor.patch`，可带预发布标记（如 `2.0.0-rc.1`），按 semver 规则排序：
预发布低于同版本正式版；数字标识按数值比较且低于字母标识；同前缀短者更小。

约束为逗号分隔的比较式：`>=`、`<=`、`>`、`<`、`==`、`!=`；裸版本按 `==` 处理；`*` 或空串表示任意版本。
示例：`">=1.0.0,<3.0.0"`、`"==1.2.3-rc.1"`、`"!=1.5.0"`。

## 决策规则（按优先级）

1. **最小化改变包数**：与旧锁相比，版本不同（或新增）的包最少。剪除是根需求收缩后的强制结果，
   不参与该优化的选择，但会计入变更报告。
2. **同成本下偏好较新版本**：改变包数相同时，按包名字典序排列的版本向量逐位取较新者。

## 明确策略

- **共享依赖最小改动**：升级一个共享依赖即可满足新约束时，求解器不会升级其他包
  （由目标 1 的分支限界保证；见 `test_upgrade_shared_dependency_only_not_everything`）。
- **删去根依赖后的保留策略**：以删除后的根需求为起点做可达性分析，凡不可达的传递依赖一律从锁中
  **剪除（prune）**，并在报告中逐包说明 "no remaining package requires it"。没有"保留孤立包"模式。
- **锁定的不存在版本**：锁中出现索引里没有的包/版本时，求解前即报输入错误，退出码 `3`。

## 不可解 vs 搜索上限

- 有限搜索空间被完整遍历而无方案 → 状态 `unsat`，退出码 `1`。
- `--max-states`（默认 100000）状态预算耗尽仍未证明无解 → 状态 `limit`，退出码 `2`，
  且明确提示"无法证明不可解"，不输出锁文件。
- 输入非法（JSON/版本格式错误、锁定版本不存在）→ 退出码 `3`。
- 成功 → 退出码 `0`。

## 因果链

每个变更包给出直接原因（哪个根需求或已变更父包的哪条约束导致旧版本被排除），
并递归向上追溯父包变更，直到根需求。例如 `sock` 的变更链：
`sock ← http-lib@2.1.0 要求 "sock>=2.0.0" ← app-server@2.1.0 要求 http-lib 升级 ← root 要求 app-server>=2.0.0`。

## 运行样例

### 1. 新约束下最小改动 + 同成本选新版（成功）

```
$ python3.11 depres.py resolve --packages examples/packages.json \
    --build examples/build.json --lock examples/lock.json
```

```text
status: ok
states explored: 14 (limit 100000)
changed packages: 4 (3 changed/added, 1 pruned)

causal chains (root requirement -> affected package):
  app-server: 1.4.0 -> 2.1.0
    because root requires "app-server>=2.0.0"; locked 1.4.0 does not satisfy
  http-lib: 1.8.0 -> 2.1.0
    because app-server@2.1.0 requires "http-lib>=2.1.0,<3.0.0"; locked 1.8.0 does not satisfy
    app-server: 1.4.0 -> 2.1.0
      because root requires "app-server>=2.0.0"; locked 1.4.0 does not satisfy
  sock: 1.2.0 -> 2.0.0
    because http-lib@2.1.0 requires "sock>=2.0.0"; locked 1.2.0 does not satisfy
    http-lib: 1.8.0 -> 2.1.0
      because app-server@2.1.0 requires "http-lib>=2.1.0,<3.0.0"; locked 1.8.0 does not satisfy
      app-server: 1.4.0 -> 2.1.0
        because root requires "app-server>=2.0.0"; locked 1.4.0 does not satisfy

pruned packages (unreachable from root requirements):
  metrics: removed (was 0.9.0)
    because no remaining package requires it
    (retention policy: after a root requirement is removed, every package no longer reachable from the remaining root requirements is pruned)

unchanged: json-util@1.1.0

new lock:
{
  "app-server": "2.1.0",
  "http-lib": "2.1.0",
  "json-util": "1.1.0",
  "sock": "2.0.0"
}
```

新根约束 `app-server>=2.0.0` 只强制 3 个包变更：`app-server`、`http-lib`、`sock`；
`json-util` 保持 `1.1.0`。`app-server@2.1.0` 与 `2.0.0` 改动包数相同，按规则 2 取新版；
旧锁中孤立的 `metrics` 被剪除。

### 2. 删去根依赖，不可达传递依赖被剪除

```
$ python3.11 depres.py resolve --packages examples/packages.json \
    --build examples/build-no-server.json --lock examples/lock.json
```

```text
status: ok
states explored: 2 (limit 100000)
changed packages: 4 (0 changed/added, 4 pruned)

pruned packages (unreachable from root requirements):
  app-server: removed (was 1.4.0)
    because no remaining package requires it
    (retention policy: after a root requirement is removed, every package no longer reachable from the remaining root requirements is pruned)
  http-lib: removed (was 1.8.0)
    because no remaining package requires it
    (retention policy: after a root requirement is removed, every package no longer reachable from the remaining root requirements is pruned)
  metrics: removed (was 0.9.0)
    because no remaining package requires it
    (retention policy: after a root requirement is removed, every package no longer reachable from the remaining root requirements is pruned)
  sock: removed (was 1.2.0)
    because no remaining package requires it
    (retention policy: after a root requirement is removed, every package no longer reachable from the remaining root requirements is pruned)

unchanged: json-util@1.1.0

new lock:
{
  "json-util": "1.1.0"
}
```

### 3. 约束不可满足（退出码 1）

```
$ python3.11 depres.py resolve --packages examples/packages.json \
    --build examples/build-unsat.json --lock examples/lock.json; echo "exit=$?"
```

stdout：

```text
status: unsat
states explored: 1 (limit 100000)
```

stderr：

```text
result: unsatisfiable - search space exhausted with no solution
exit=1
```

### 4. 搜索预算耗尽（退出码 2，区别于不可解）

```
$ python3.11 depres.py resolve --packages examples/packages.json \
    --build examples/build.json --lock examples/lock.json --max-states 1; echo "exit=$?"
```

stdout：

```text
status: limit
states explored: 2 (limit 1)
```

stderr：

```text
result: search limit exceeded after 2 states; unsatisfiability cannot be proven
exit=2
```

### 5. 锁中版本在索引中不存在（退出码 3）

```
$ python3.11 depres.py resolve --packages examples/packages.json \
    --build examples/build.json --lock examples/lock-missing.json; echo "exit=$?"
```

stderr：

```text
input error: lock file pins app-server@9.9.9, but that version does not exist in the package index
exit=3
```

写入新锁文件可加 `--out lock.new.json`；不带 `--lock` 时按无旧锁解析（所有解析版本均计为新增）。

## 测试结果

```
$ python3.11 -m unittest -v
```

覆盖：版本/预发布排序、约束匹配、锁仍满足时零改动、最小改动+新版偏好、共享依赖单独升级、
少改动优先于新版本、因果链内容、根删除剪除策略、不可解与搜索上限区分、未知包/越界版本、
锁定不存在版本报错，以及 CLI 四种退出码。
