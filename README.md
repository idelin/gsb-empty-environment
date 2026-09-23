# sturm_iso — 有理系数多项式 Sturm 序列实根隔离

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理运算），不调用任何符号代数库。

## 功能

- 对 Q[x] 单变量多项式用 Sturm 序列隔离**不同实根**；重数经 Yun 无平方分解单独列出（`x^2` 只有根 0，重数 2 另列）。
- 端点恰根用精确规则处理：区间一律为半开 `(lo, hi]`（左排右含），`V(a)-V(b)` 对任意端点成立；二分中点恰为根时提取为**点根**，左区间记 `hi_excluded` 并精确减 1，计数一致。
- 每个根附带证书：所属无平方因子、重数、端点符号序列与符号变化数、根数 `V(lo)-V(hi)(-1) = 1`。
- 独立验证器 `verify_isolation` / `verify_certificate` 从零重算分解、Sturm 序列、端点符号、区间不相交性与完备性（已隔离 + 未决计数 == 全局 Sturm 总数）。
- 预算中断：`isolate(p, max_steps=k)` 预算耗尽时返回已隔离根与未决区间（含各自根数证书）。
- 多项式参数修订：`revise_isolation(old, p_new)` 在新多项式上**重用旧区间**重新精确计数；身份匹配只依据区间包含关系——计数 1 保持、计数 >1 记为集合级 split、计数 0 记为 vanished、覆盖之外记为 new；重根分裂/合并**不按区间中心距离猜唯一身份**，只给出 split/merge 候选标注。
- 无实根返回空列表；零多项式抛 `ValueError`。
- 每步变换的适用域说明附在 `IsolationResult.notes` / `RevisionResult.notes`。

## 文件

- `sturm_iso/polynomials.py` — Q[x] 精确运算：带余除法、gcd、形式导数、Yun 无平方分解
- `sturm_iso/sturm.py` — Sturm 序列、符号变化计数、Cauchy 根界
- `sturm_iso/engine.py` — 预算感知隔离引擎、点根/区间根证书
- `sturm_iso/verify.py` — 独立验证器
- `sturm_iso/revision.py` — 参数修订、区间重用、身份匹配
- `demo.py` — 运行样例（9 个场景）
- `tests/test_sturm_iso.py` — 19 个 unittest

## 运行

```
python3 demo.py
python3 -m unittest discover -s tests -v
```

## 运行样例（节选）

```
=== 示例 1: x^2 —— 只有根 0，重数另列 ===
p(x) = x^2
无平方部分 q(x) = x
不同实根总数(Sturm 证明): 1  完成: True
  [区间根 r1] x in (-1, 1]  重数 = 2  因子 = x
      证书: V(lo)=1 符号(-1, 1)  V(hi)=0 符号(1, 1)  根数 = V(lo)-V(hi) = 1
独立验证器: 通过

=== 示例 4: x(x-1)(x-2) —— 端点恰根的精确处理 ===
不同实根总数(Sturm 证明): 3  完成: True
  [点根 r1] x = 0  重数 = 1
  [区间根 r3] x in (0, 2)  重数 = 1
      证书: V(lo)=2 符号(1, -1, 1)  V(hi)=0 符号(1, 1, 1)  根数 = V(lo)-V(hi)-1(端点恰根) = 1
  [点根 r2] x = 2  重数 = 1
独立验证器: 通过

=== 示例 6: 预算中断(max_steps=1)，返回已隔离+未决 ===
不同实根总数(Sturm 证明): 4  完成: False
  [未决区间] (0, 51]  根数 = 4  (V(lo)=4, V(hi)=0)
独立验证器: 通过

=== 示例 7: (x^2-2)^2 -> (x^2-2)^2 - 1/100 —— 重根分裂 ===
身份匹配:
  [split] 旧('r1',) -> 新('n1', 'n2')   后代身份为不确定集合，不指定唯一对应
  [split] 旧('r2',) -> 新('n3', 'n4')

=== 示例 9: x^2-2 -> x^2-199/100 —— 区间重用，身份保持 ===
重用区间数: 2
  [preserved] 旧('r1',) -> 新('r1',)   身份经包含关系保持（非距离猜测）
  [preserved] 旧('r2',) -> 新('r2',)
```

## 测试结果

```
Ran 19 tests in 0.019s
OK
```
