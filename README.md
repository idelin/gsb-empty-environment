# feature-resolver

从**本地 JSON**（不联网）计算特性（feature）与包的**联合闭包**：

- 依赖可由**特性布尔表达式**（`and` / `or` / `not` / 括号）启用；
- 区分**默认特性**、**显式禁用**、**强制开启**三种状态，并在输出中给出每个特性的来源；
- 激活语义为从根种子出发的**最小不动点**：仅循环互相开启、没有根种子的特性/包**不能激活**，并作为诊断（warning）报告；
- **显式禁用与传递必需冲突**时报错，并给出完整来源链；
- 相同特性重复请求**幂等**；
- 版本为 `major.minor.patch[-prerelease]`，按 semver 优先级比较，每个包名选取满足全部约束的最高版本。

仅使用 Python 标准库（3.11+），测试基于 `unittest`。

## 输入格式

### 注册表 `registry.json`

```json
{
  "packages": [
    {
      "name": "app",
      "version": "1.0.0",
      "features": { "default": ["cli"], "net": ["tls"], "tls": [] },
      "dependencies": [
        {
          "name": "http",
          "req": ">=1.0.0, <2.0.0",
          "optional": true,
          "features": ["client"],
          "disable": [],
          "default-features": true,
          "when": "net and not minimal"
        }
      ]
    }
  ]
}
```

- `features`：特性 → 它蕴含的特性列表；`default` 是默认特性。
- `dependencies[].when`：可选；省略时，非可选依赖恒激活，可选依赖由同名特性激活（隐式特性）。
- `req`：逗号分隔的比较子合取，支持 `>= <= > < == != =` 与裸版本（等价于 `==`），`*` 匹配任意版本。
- 预发布版本按 semver 规则参与普通比较（如 `1.6.0-rc.1 < 1.6.0`，且 `> 1.5.0`）。

### 构建输入 `request.json`

```json
{
  "root": "app",
  "req": ">=1.0.0",
  "features": ["net"],
  "disable": ["tls"],
  "default_features": true
}
```

`features` 为强制开启（根种子），`disable` 为显式禁用，`default_features` 控制是否播种默认特性。

## 运行样例

```console
$ python3 -m feature_resolver examples/registry.json examples/request.json
```

输出（节选，完整见下）：

```json
{
  "root": "app",
  "packages": [
    {
      "name": "app",
      "version": "1.0.0",
      "features": ["cli", "default", "loop", "net", "ping", "pong", "tls"],
      "origins": {
        "forced": ["loop", "net"],
        "default": ["default"],
        "implied": ["cli", "ping", "pong", "tls"]
      },
      "disabled": []
    },
    { "name": "http", "version": "1.4.0-beta.1", "...": "..." },
    { "name": "util", "version": "0.4.0", "...": "..." }
  ],
  "diagnostics": [
    {
      "level": "warning",
      "kind": "feature-cycle",
      "message": "self-supporting cycle without a root seed; it cannot activate: feature left/spin -> feature right/spin",
      "cycle": ["feature left/spin", "feature right/spin"]
    },
    {
      "level": "info",
      "kind": "feature-cycle",
      "message": "cyclic derivation grounded by a root seed: feature app/ping -> feature app/pong",
      "cycle": ["feature app/ping", "feature app/pong"]
    }
  ]
}
```

说明：

- 请求中 `net` 重复写了两次（幂等）；`net and not minimal` 为真，故 `http` 被激活并选中了满足 `>=1.0.0, <2.0.0` 的最高版本 `1.4.0-beta.1`；`util` 选中 `0.4.0`。
- `left/spin` 与 `right/spin` 互相开启但无根种子 → 不激活，产生 warning。
- `app/ping ↔ app/pong` 也是循环，但被 `net` 播种 → 正常激活，仅记 info。

### 冲突样例（显式禁用 vs 传递必需）

```console
$ python3 -m feature_resolver examples/registry.json examples/request-conflict.json
error: feature conflict in package 'app' 1.0.0:
  feature 'tls' is explicitly disabled by root request
  but it is transitively required:
    - feature 'tls' implied by 'net' of package 'app'
    - feature 'net' required by root request
$ echo $?
1
```

## 测试结果

```console
$ python3 -m unittest discover -s tests -t .
............................................
----------------------------------------------------------------------
Ran 44 tests in 0.003s

OK
```

覆盖：版本解析/预发布排序/需求匹配、布尔表达式解析与求值、默认特性播种与关闭、
强制开启与传递蕴含、布尔表达式门控可选依赖、隐式特性、显式禁用冲突的来源链
（根请求与依赖边两种）、重复请求幂等、无根种子循环不激活并告警、有种子循环正常
激活、最高匹配版本选择、约束不可满足/未知包/未知特性报错。

## 代码结构

- `feature_resolver/version.py` — semver 版本与需求解析、比较
- `feature_resolver/expr.py` — 特性布尔表达式（解析/求值/渲染）
- `feature_resolver/model.py` — 注册表与构建请求的 JSON 加载与校验
- `feature_resolver/resolver.py` — 闭包求解（种子传播、冲突来源链、Tarjan SCC 循环检测）
- `feature_resolver/__main__.py` — CLI 入口
- `examples/` — 示例注册表与请求（含冲突样例）
- `tests/` — `unittest` 测试（44 个）
