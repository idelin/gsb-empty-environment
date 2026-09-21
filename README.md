# depsolver — 基于锁文件的最小变更依赖求解器

纯 Python 3.11 标准库实现，无第三方依赖，不联网。所有包信息与构建输入均来自本地 JSON 文件。

## 核心策略（显式约定）

- **目标函数（字典序）**：先最小化相对锁文件的变更包数（升级 / 降级 / 新增 / 删除各计 1 次），再偏好较新版本（按包名字典序逐包比较，版本高者优）。因此只改一个共享依赖即可满足新约束时，绝不会升级全部包。
- **版本格式**：`major.minor.patch`，可带预发布后缀（如 `1.2.3-beta.1`），按 semver 2.0 规则排序（预发布 < 同基线正式版；数值标识符 < 字母标识符）。约束把预发布版本当作普通版本参与比较。
- **约束语法**：逗号分隔的比较子，支持 `>=` `<=` `==` `!=` `>` `<`；空串或 `*` 表示任意版本。
- **孤儿策略（删去根依赖后）**：默认 `prune`——从根需求不可达的锁定包从新锁中删除（计为一次 `removed` 变更）；`--keep-orphans` 则将其按锁定版本原样保留并在报告中标注。
- **锁校验**：锁定包或锁定版本在仓库中不存在时直接报错（`lock error`，退出码 3），不静默忽略。
- **结果区分**：
  - `solved`：搜索空间已穷尽，解是最优的（变更数最少）。
  - `unsatisfiable`：搜索空间已穷尽，证明无解，并报告冲突。
  - `search-limit-reached`：搜索预算（`--max-states`）耗尽，结果不完整；若已找到解则标注 "best-so-far, not proven optimal"。

## 输入格式

仓库（`--repo`）：

```json
{
  "packages": {
    "app": {
      "1.0.0": {"dependencies": {"lib": ">=1.0.0,<2.0.0"}}
    },
    "lib": {"1.4.0": {}}
  }
}
```

锁文件（`--lock`）：`{"locked": {"app": "1.0.0", "lib": "1.4.0"}}`

根需求 / 构建输入（`--requirements`）：`{"requirements": {"lib": ">=2.0.0"}}`

## 用法

```bash
python3.11 -m depsolver resolve \
  --repo examples/repo.json \
  --lock examples/lock.json \
  --requirements examples/requirements.json \
  [--max-states 100000] [--keep-orphans] [--write-lock new-lock.json] [--json]
```

退出码：`0` 已解出；`1` 不可解（已证明）；`2` 达到搜索上限；`3` 输入 / 锁文件错误。

## 运行样例（实际输出）

### 样例 1：新增约束 `lib>=2.0.0`，只改必须改的包

`util==0.9.0` 被钉住不动（尽管仓库里有 0.9.1），`app` 因级联冲突被迫升级：

```
$ python3.11 -m depsolver resolve --repo examples/repo.json --lock examples/lock.json --requirements examples/requirements.json
status: solved (optimal: search space exhausted)
explored states: 6
changes (2):
  app: 1.0.0 -> 1.1.0 (upgraded)
    why: locked 1.0.0 satisfies its own constraints but leads to conflict: lib: no available version satisfies root requirement requires lib >=2.0.0; app 1.0.0 requires lib >=1.0.0,<2.0.0 (available: 1.4.0, 2.0.0, 2.1.0)
    chain: root -> app
    selected: 1.1.0 is the newest version satisfying all active constraints (policy: minimal changes, then prefer newer)
  lib: 1.4.0 -> 2.1.0 (upgraded)
    why: locked 1.4.0 violates ">=2.0.0" from root requirement
    chain: root -> lib
    selected: 2.1.0 is the newest version satisfying all active constraints (policy: minimal changes, then prefer newer)
new lock:
  app: 1.1.0
  lib: 2.1.0
  util: 0.9.0
exit=0
```

### 样例 2：不可解（已证明，区别于搜索上限）

`requirements-unsat.json` 要求 `app==1.0.0` 且 `lib>=2.0.0`，而 `app 1.0.0` 依赖 `lib<2.0.0`：

```
status: unsatisfiable (proven: search space exhausted)
explored states: 2
conflicts (most recent first):
  lib: no available version satisfies root requirement requires lib >=2.0.0; app 1.0.0 requires lib >=1.0.0,<2.0.0 (available: 1.4.0, 2.0.0, 2.1.0)
exit=1
```

### 样例 3：搜索上限（不完整的诚实报告）

```
$ python3.11 -m depsolver resolve ... --max-states 2
status: search-limit-reached (incomplete: no proven result; raise --max-states)
explored states: 3
exit=2
```

### 样例 4：锁定版本不存在 → 报错

```
$ python3.11 -m depsolver resolve ... --lock bad-lock.json   # lib 锁定 9.9.9
lock error: locked version lib 9.9.9 is not in the repository
exit=3
```

### 样例 5：删去根依赖后的孤儿策略

从需求中去掉 `util` 后，默认 `prune`：`util: 0.9.0 -> (removed)`，`why: no longer reachable from the root requirements (orphan policy: prune)`；加 `--keep-orphans` 则新锁中保留 `util: 0.9.0  # kept orphan (policy: keep)`。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

结果：`Ran 33 tests ... OK`（版本/约束解析、最小变更目标、级联升级与因果链、不可解 vs 搜索上限、锁校验报错、孤儿策略两种模式、预发布排序、CLI 退出码与 `--write-lock`/`--json`）。

## 代码结构

- `depsolver/version.py` — semver 解析与比较（含预发布）
- `depsolver/constraints.py` — 约束解析与判定
- `depsolver/model.py` — 仓库 / 锁 / 需求 JSON 加载与校验
- `depsolver/solver.py` — 回溯搜索：最小变更目标、分支限界、搜索预算、锁校验、因果记录
- `depsolver/report.py` — 每项变更的因果链报告
- `depsolver/cli.py` — 命令行入口与退出码
