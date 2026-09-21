# license-solver

许可表达式解析 + 全依赖图选择求解器。纯 Python 3.11 标准库，测试用 `unittest`，
不联网查包，包与构建输入全部来自本地 JSON。

**免责声明**：所有传播 / 披露 / 例外规则都是题内合成的建模规则（见 `samples/*.json`
的 `rules` 节），不构成任何法律结论。

## 运行

    python3 main.py                              # 可运行样例（5 组演示）
    python3 -m unittest discover -s tests -v     # 测试

## 语义

- **AND**：两侧义务取并集，同时生效。
- **OR**：全局选择点。不能每包局部贪心——分支选择会约束传递依赖的可用版本，
  求解器在整图上做分支定界（见演示 1 的三包样例 `left/right/shared`：
  局部都合法、全图冲突，可行替代分支是 `left@1.0.0` 取 MIT 侧）。
- **WITH**：许可 + 例外绑定为一个节点。例外只在目标条件（如
  `independent_modules`）成立时修改义务；条件不成立时按基础许可全额生效，
  绝不拆成无条件允许。

## 求解

- 目标按字典序：先最少**例外**包数（合成规则：如专有目标内出现强 copyleft
  标签需豁免），再最少需**披露**包数；共享依赖在选择中只有一个版本，只计一次。
- 传播规则按边生效：标签（如 `copyleft-strong/weak`）沿依赖边向上传播，
  边的链接方式（静态 / 动态，可按边覆盖目标级设置）决定规则是否触发；
  目标条件（`attributes`）决定例外规则与 WITH 条件是否成立。
- 搜索预算耗尽时：已找到可行解则返回 `feasible_upper_bound`（最优性未决，
  只对返回的选择本身验证过约束）；否则返回 `unknown`。不会声称通过全部约束。
- `license_solver.bruteforce.brute_force` 对小图做笛卡尔积全枚举，
  `tests/test_solver.py` 用 60 个随机小图与分支定界求解器交叉比对最优值。

## 增量修复与冲突核

`license_solver.repair.repair(repo, build, previous, forced)`：

1. 计算被变更包的反向依赖锥，锥外包的旧选择保持钉住，只重解锥内；
2. 局部修复不可行时，对钉住的选择做删除极小化，输出冲突核
   （如 `["forced shared@1.1.0", "pin plugin@1.0.0"]`）；
3. 回退到全量重解（仍保留强制变更）。

## 输入格式

- 仓库 JSON（`samples/repo.json`）：`licenses`（义务 / 标签）、`exceptions`
  （WITH 例外的条件与移除项）、`packages.<name>.versions[]`，每个版本含
  `version`（`major.minor.patch[-prerelease]`）、`license`（表达式）、
  `deps`（值为范围字符串或 `{"range": ..., "link": ...}`）。
- 构建 JSON（`samples/build_trap.json`）：`target`、`link`、`attributes`、
  `roots`（根包及范围）、`rules`（`propagation` / `incompatible` /
  `exception_required`）。
- 版本范围：`*`、精确版本、`^1.2.0`、`>=1.0.0 <2.0.0` 等比较子合取。

## 结构

    license_solver/
      version.py     # major.minor.patch[-prerelease] 与版本范围
      expression.py  # AND / OR / WITH 解析与求值
      model.py       # 本地 JSON 加载
      solver.py      # 分支定界求解器（预算、钉住、部分评估剪枝）
      bruteforce.py  # 小图全枚举对照
      greedy.py      # 局部贪心（演示其失败用）
      repair.py      # 增量修复 + 冲突核
    samples/         # 仓库与构建输入样例
    tests/           # unittest
    main.py          # 可运行演示
