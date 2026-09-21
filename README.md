# feature_resolver

特性/依赖联合闭包求解器。包与构建输入全部来自本地 JSON，不联网；仅使用
Python 3.11 标准库，测试基于 `unittest`。

## 语义

- **联合闭包**：从根请求（root seed）出发做最小不动点传播，把多个请求者
  对同一 `name@version` 的特性求并集，得到激活特性与依赖包的闭包。
- **默认特性**：包的 `default_features` 在根或依赖未声明
  `"default_features": false` 时自动激活。
- **显式禁用**：根或依赖可 `disable` 某特性；被禁用的特性永不激活
  （默认特性被显式禁用是合法的，直接跳过）。
- **强制开启**：任何激活规则（`enables` / 条件规则 / 依赖请求）都是必需
  蕴含；若其目标被显式禁用则产生**冲突**，报错信息带完整来源链
  （谁禁用、经哪条链被要求）。
- **循环推导**：仅互相开启、无根种子的特性环**不会**激活（最小不动点
  天然保证）；被种子激活的环正常生效，并在输出的 `cycles` 中报告
  （Tarjan SCC，含跨包环）。
- **幂等**：同一特性重复请求结果不变（集合语义 + 不动点与顺序无关）。
- **版本**：`major.minor.patch[-prerelease]`，预发布按 semver 规则排序；
  需求支持 `=`（裸写默认）、`^`、`~`、`>=`、`<=`、`>`、`<`，
  在本地包集中选满足条件的最高版本。

## 输入格式

```json
{
  "packages": [
    {
      "name": "app",
      "version": "1.4.0",
      "default_features": ["std"],
      "features": {
        "std": { "enables": ["alloc"] },
        "tls": {
          "enables": ["net"],
          "conditional": [
            { "when": "std && !minimal", "enables": ["prom"],
              "deps": [ { "package": "io", "version": "^1.0.0" } ] }
          ],
          "deps": [
            { "package": "tlslib", "version": "3.1.2",
              "features": ["x509"], "default_features": false,
              "disable": ["ocsp"] }
          ]
        }
      }
    }
  ],
  "build": {
    "roots": [
      { "package": "app", "version": "^1.0.0",
        "enable": ["tls"], "disable": ["minimal"],
        "default_features": true }
    ]
  }
}
```

布尔表达式支持 `&&`、`||`、`!`、括号，标识符为特性名。

## 运行

```console
$ python3 -m feature_resolver examples/sample.json
{
  "ok": true,
  "packages": [
    {
      "name": "app",
      "version": "1.4.0",
      "features": ["alloc", "cycle_a", "cycle_b", "metrics", "net", "prom", "std", "tls"],
      "disabled": ["minimal"],
      "default_features_applied": ["std"]
    },
    { "name": "netcore", "version": "2.3.1", "features": ["tcp"], ... },
    { "name": "tlslib", "version": "3.1.2", "features": ["x509"], ... }
  ],
  "cycles": [["app@1.4.0:cycle_a", "app@1.4.0:cycle_b"]],
  "conflicts": []
}
```

样例中：`loop_a`/`loop_b` 互相开启但无根种子，保持未激活；
`cycle_a` 被根请求播种，环被激活并报告；`metrics` 的条件表达式
`std && !minimal` 成立，派生 `prom`；`netcore` 按 `^2.0.0` 选中 2.3.1；
`tlslib` 精确匹配 3.1.2（跳过预发布 `3.1.2-rc.1`）且未应用默认特性。

冲突示例（`examples/conflict.json`：根显式禁用 `alloc`，而默认特性 `std`
强制开启它），退出码为 1：

```console
$ python3 -m feature_resolver examples/conflict.json
...
  "conflicts": [
    "conflict: app@1.4.0:alloc is explicitly disabled by root request, but required via app@1.4.0:alloc <- app@1.4.0:std <- default features of app@1.4.0"
  ]
```

退出码：0 成功，1 存在冲突，2 输入/模型错误。

## 测试

```console
$ python3 -m unittest discover -s tests -v
...
Ran 22 tests in 0.003s

OK
```

覆盖：版本解析/预发布排序/需求匹配，布尔表达式优先级，传递闭包与多根
并集，跨包依赖闭包与最高版本选择，条件依赖，默认特性开关，显式禁用，
禁用 vs 传递必需的冲突溯源（根级与依赖级），无种子环不激活、有种子环
激活并报告、跨包环检测，重复请求幂等，非法输入校验。

## 结构

- `feature_resolver/version.py` — semver 版本与需求匹配
- `feature_resolver/expr.py` — 特性布尔表达式解析/求值
- `feature_resolver/model.py` — 本地 JSON 模型加载与校验
- `feature_resolver/resolver.py` — 不动点闭包、冲突溯源、SCC 环检测
- `feature_resolver/__main__.py` — CLI
- `examples/` — 正常与冲突两个样例
- `tests/test_resolver.py` — 22 个 unittest 用例
