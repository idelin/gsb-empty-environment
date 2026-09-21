# 能力解析器（capability resolver）

声明式包解析：同一能力可由多个实现提供，解析器综合**版本约束、互斥（conflicts）、
目标（target）约束**选出每个能力的唯一实现，输出「能力 → 实现」映射。
全部输入来自本地 JSON，不联网查包。仅使用 Python 3.11 标准库。

## 输入格式

`data/packages.json` —— 包仓库（数组）：

```json
{
  "name": "fastjson",
  "version": "2.1.0",                  // major.minor.patch，可带预发布：1.0.0-rc.1
  "provides": ["json"],                // 提供的能力
  "conflicts": ["simplejson"],         // 互斥的包名（双向生效）
  "targets": ["linux", "macos"],       // 支持的目标；缺省表示全部支持
  "requires": {"json": ">=2.0.0"}      // 依赖的能力及版本约束
}
```

`data/build.json` —— 构建输入：

```json
{"target": "linux", "requires": {"json": null, "logging": "<2.0.0"}}
```

版本约束为逗号分隔的子句：`>=`、`<=`、`>`、`<`、`==`、`!=`；`null` 表示不限。

## 选择与稳定决胜规则

1. 能力按**字母序**依次解析。
2. 候选实现按**版本降序、包名升序**的稳定顺序尝试。
3. 候选须通过全部过滤：版本约束、目标匹配、不与已选包互斥、
   同一能力保持**单提供者**、同一包名至多一个版本。
4. 冲突或依赖不满足时**回溯**（A 冲突则尝试 B），取第一个完整解；
   结果只取决于上述规则，与输入顺序无关（确定性）。
5. 替代链成环时通过 `visiting` 集合拒绝重复进入，**不会无限递归**；
   每次递归至少新增一个已分配能力，保证终止。

## 运行

```console
$ python3.11 main.py data/packages.json data/build.json
json -> fastjson@2.1.0
logging -> logger-a@1.0.0
widget -> widget-b@2.5.0
```

解读：`winjson@3.0.0` 虽版本最高但仅支持 windows 被目标约束过滤；
`logger-b@1.1.0` 需要 `json<2.0.0` 与已选 `fastjson@2.1.0` 冲突，回退到
`logger-a`；`widget-a` 与 `fastjson` 互斥，改用 `widget-b`。

成环样例（正常终止）：

```console
$ python3.11 main.py data/packages.json data/build-cycle.json
capA -> cycle-a@1.0.0
capB -> cycle-b@1.0.0
```

无解时退出码为 1 并输出原因。

## 测试

```console
$ python3.11 -m unittest -v
...
Ran 19 tests in 0.001s

OK
```

覆盖：语义化版本与预发布排序、版本约束、最高版本优先、同名决胜、
目标过滤、互斥回退、单提供者、跨能力回溯、成环终止、无解报错、结果确定性。
