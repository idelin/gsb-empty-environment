# cfgdoctor — CFG 无用规则 / epsilon 环 / 增量修复分析器

纯 Python 3.11 标准库实现(无正则/自动机库),字母表与产生式(转移结构)
全部显式输入并在构造时校验封闭性。

## 运行

```bash
python3 samples.py                  # 运行样例(5 个诊断样例 + 增量演示 + 枚举非证明演示)
python3 -m unittest test_cfgdoctor  # 测试(35 个用例)
```

## 功能

- **可生成性最小不动点证书**(`generating.py`):每个可生成符号携带轮次(秩)、
  见证产生式与见证终结串;`verify.verify_generating_certificate` 独立复核
  "秩严格下降 + 补集封闭"两组条件,恰好刻画最小不动点。
- **不可达分析**(`reachability.py` 主分析 BFS,`verify.independent_reachable`
  独立 DFS 交叉复核)。
- **epsilon 环诊断**(`cycles.py`):依赖边要求侧余全部可空(A ⇒+ A)。
  分类:死循环(仅 A→B, B→A 之类,不可生成)/ 可空环另有终结出口(规则有用,
  不可整体删除)/ epsilon 上下文环。每个环给出可独立执行的再生见证 A ⇒+ A。
- **无限歧义 vs 语言无限分别诊断**:同一终结串的无限推导(epsilon 环)
  不蕴含语言无限;语言无限性由泵送结构(含宽边的依赖环)独立判定。
- **最短问题链**(`chains.py`):不可生成符号连接到最近的缺陷核心
  (无产生式符号 / 自循环 / 无终结出口的死循环);不可达符号给出引用者诊断。
- **等价简化**(`simplify.py`):先删不可生成、再删不可达,输出逐条规则的
  keep/drop 清理映射与旧→新编号映射;开始符无用时返回空语言说明(L(G)=∅)。
- **增量修复**(`incremental.py`):规则撤回后按 DRed 风格"过度删除 +
  局部重导出"修复可生成/可达状态,循环互依的支撑关系不会被误判为有效。
- **交叉核验**(`bounded_enum.py`):有界推导枚举在精确界(见证推导步数)内
  复现每个证书见证串;负向只做一致性观察——枚举找不到 ≠ 不可生成,
  不可生成的证明只来自不动点证书。
- **反例可独立执行**(`verify.py`):`run_derivation` 逐步执行任意推导步序列,
  证书见证、循环见证均可脱离分析器独立验证。

## 模块

| 文件 | 职责 |
| --- | --- |
| `cfgdoctor/grammar.py` | 显式字母表/产生式模型与校验 |
| `cfgdoctor/generating.py` | 可空/可生成最小不动点与证书 |
| `cfgdoctor/reachability.py` | 可达性主分析 |
| `cfgdoctor/cycles.py` | epsilon 环、语言无限性 |
| `cfgdoctor/chains.py` | 最短问题链 |
| `cfgdoctor/simplify.py` | 无用规则清理映射与等价简化 |
| `cfgdoctor/incremental.py` | 规则撤回后的增量修复 |
| `cfgdoctor/bounded_enum.py` | 有界枚举与交叉核验 |
| `cfgdoctor/verify.py` | 独立验证器/反例执行器 |
| `cfgdoctor/report.py` | 汇总报告 |
