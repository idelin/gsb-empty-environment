# licsel — 许可表达式解析与全依赖图选择求解

Python 3.11，仅用标准库，测试用 `unittest`。包与构建输入全部来自本地 JSON，不联网。
所有传播规则都是**输入文件里声明的合成规则**，求解结果不构成任何法律结论。

## 语义

- **AND**：所有分支同时成立，合并进同一个项（term）。
- **OR**：恰好选一个分支，是求解器的选择点；选择是**全图**的，不能逐包局部贪心
  （见 `samples/three_package_global.json`：libX 局部最优选 BSD，但与传递依赖 libY 的
  GPL-3.0 在合成规则下冲突，唯一可行分支是 libX=GPL-3.0）。
- **WITH**：例外只附着在左侧单个许可原子上，仅当该分支被选中才生效；
  不是无条件允许（见 `samples/with_exception.json`，无例外的 GPL-2.0 分支不可行）。

版本为 `major.minor.patch[-prerelease]`，排序遵循 semver（预发布低于正式版，
数字标识符按数值比较且排在字母标识符之前）。依赖约束如 `>=1.0.0 <2.0.0`，
解析时从本地索引取满足约束的最高版本。

## 目标与状态

目标按字典序：**先最少 WITH 例外数，再最少需披露的包数**；共享依赖是图中同一节点，只计一次。

- `optimal`：搜索完成，所有规则实例成立且目标最小。
- `feasible_upper_bound`：预算耗尽，已找到可行解但只是**上界**，`complete=false`，
  最优性未证、全部约束验证未决——不声称通过全部约束。
- `unknown`：预算耗尽且尚无可行解，可行性未决。
- `infeasible`：搜索完成无可行解，附删除极小的**冲突核**
  （规则实例集合：规则 id、触发包、作用域包）。

## 用法

```sh
python3.11 main.py solve      samples/three_package_global.json
python3.11 main.py solve      samples/budget.json --budget 12
python3.11 main.py enumerate  samples/three_package_global.json   # 小图暴力枚举对拍
python3.11 main.py solve      samples/repair_v1.json > prev.json  # 基线求解
python3.11 main.py repair     samples/repair_v2.json --prev prev.json  # 版本变更后增量修复
python3.11 main.py solve      input.json --pin libA=1.0.0         # 强制解析到指定版本
./run_samples.sh                                                  # 一键跑全部样例
python3.11 -m unittest discover -s tests -v                       # 测试
```

增量修复：上一解中许可项未变的包被钉住（pinned），只重搜真正自由的变量；
钉住后不可行则释放全部钉、以旧选择为优先序热启动重解；仍不可行则输出冲突核。

## 输入 JSON 结构

```jsonc
{
  "rules": {
    "license_tags": {"GPL-3.0": ["strong-copyleft"], "MIT": ["permissive"]},
    "disclosure_tags": ["strong-copyleft", "weak-copyleft"],
    "propagation": [{
      "id": "static-strong-copyleft-dependents",
      "trigger": {"link": "static", "tag": "strong-copyleft"},  // link 可省略=任意
      "scope": "static_dependents",      // 或 static_closure / direct_dependents
      "require_tag_in": ["strong-copyleft", "weak-copyleft"],
      "unless_exception": "Classpath-2.0",                      // 可省略
      "target_conditions": {"distribution": "binary"},          // 可省略
      "message": "..."
    }]
  },
  "targets": [{"name": "app-image", "conditions": {"distribution": "binary"},
               "link": "static", "roots": [{"name": "app", "constraint": "1.0.0"}]}],
  "packages": [{"name": "app", "version": "1.0.0", "license": "MIT",
                "dependencies": [{"name": "libX", "constraint": ">=1.0.0 <2.0.0",
                                  "link": "dynamic"}]}]
}
```

规则语义：当某包**已选**许可带 `trigger.tag`、且经 `trigger.link` 类型的边被引入、
目标条件匹配时，`scope` 内每个包的已选项中所有许可原子都必须带 `require_tag_in`
中的标签；除非触发原子上带有 `unless_exception` 指定的 WITH 例外。

## 样例

| 文件 | 演示点 |
| --- | --- |
| `samples/three_package_global.json` | 三包：局部贪心可选但全图冲突，求解器选可行替代分支 |
| `samples/with_exception.json` | WITH 例外只豁免带例外的分支，计 1 个例外 |
| `samples/diamond_shared.json` | 菱形图共享依赖只计一次披露 |
| `samples/conflict_core.json` | 不可行，输出极小冲突核（排除无关诱饵包 libQ） |
| `samples/budget.json` | 8 包链 256 组合，小预算给可行上界 + pending |
| `samples/repair_v1.json` / `repair_v2.json` | 索引新增 libA 2.0.0 后的增量修复 |

## 代码结构

- `licsel/version.py` — 版本与约束（含预发布排序）
- `licsel/expr.py` — 表达式解析（AND/OR/WITH）与 DNF 分支展开
- `licsel/model.py` — JSON 加载、依赖图、规则实例
- `licsel/solver.py` — 分支定界求解、预算控制、暴力枚举对拍
- `licsel/core.py` — 冲突核（删除极小化）
- `licsel/incremental.py` — 版本变更后的增量修复
- `main.py` — CLI；`tests/` — 49 个 unittest 用例（含随机小图枚举对拍）
