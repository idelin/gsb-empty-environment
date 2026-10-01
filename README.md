# petrinet — 有限容量 Petri 网执行与可达性验证

Python 3.11，仅标准库；测试使用 unittest。

## 模型

- **库所（Place）**：必须带有限正整数容量。无界库所在建模时直接拒绝——
  验证对象仅限给定有限状态模型，结论为精确可达性，**绝不**退化为无界网
  的 coverability 近似。
- **转移（Transition）**：四类弧分别定义（place_id → 正整数重数）：
  - `inputs` 消耗弧：需足量 token，触发时消耗；
  - `outputs` 生产弧：触发时生产，受容量约束；
  - `read` 读弧：需足量 token，但不消耗；
  - `inhibitor` 抑制弧：仅当 token 数 < 重数时启用。
- **启用条件**（`petrinet/engine.py`）：输入弧重数需足量 token；读弧、抑制弧
  分别判定；容量按**最终** token 数 `m[p] - in + out` 判定——自环的消耗与
  生产一起判（净效应），超容量转移禁用。触发是原子的：消耗与生产一步完成。

## 可达搜索（`petrinet/reachability.py`）

- BFS 精确枚举可达标记，输出标记路径（`SearchResult.path_to`，真实库所 ID）。
- 判定：`safe`（搜完无反例）/ `violated`（含可重放反例）/ `pending`
  （被 `SearchLimits` 截断，未搜索完保持未决）。
- 不变式证书在每个访问到的标记上检查；**终态（死锁）标记单独汇总**，
  token 总量按网不变量验证。
- 反例 = 转移 ID 序列，可 `replay()` 重放、可 JSON 序列化
  （`Counterexample.to_json/from_json`），重放失败抛 `ReplayError`。

## 不变式证书（`petrinet/invariants.py`）

证书是库所权重向量 w。验证（而非计算）：对每个转移 t，
`Σ w[p]·(产出−消耗) == 0`。有效证书保证 `Σ w[p]·m[p]` 在可达标记上恒定。

## 对称归约（`petrinet/symmetry.py`）

- 用户提供库所置换生成元；`verify_symmetry` 验证其为网自同构（容量保持 +
  四类弧的转移签名多重集保持），**保证归约保留启用条件**。
- 归约搜索按轨道代表元（字典序最小标记）去重，节点保存真实标记，
  反例直接落在真实库所 ID 上、可直接重放。
- `cross_check` 用独立无归约搜索对照小网：两边可达标记的轨道集必须相等。

## 增量证书重验（`petrinet/certificates.py`）

`CertificateStore` 缓存证书验证结果。`add/remove/replace_transition` 把受影响
转移记入 dirty 集；`revalidate()` 对仍有效的证书**只重验 dirty 转移**，
对失效证书做全量重验（结构修复可能使其重新成立）。

## 运行

```bash
python3.11 examples/demo.py      # 运行样例
python3.11 -m unittest discover -s tests -v   # 测试
```

## 样例输出（`examples/demo.py`）

```
=== 1. Bounded line: capacity / read / inhibitor arcs ===
net fingerprint: 8162921e446e5ced ...
certificate 'conservation' verified: True
verdict=safe  states=9  terminals=0
invariant holds on all markings: {'conservation': True}

--- batch net: terminal token total checked by invariant ---
  terminal: {'done': 2, 'raw': 0}  token total: 2  invariant ok: {'total': True}

--- property: done >= 1 ---
verdict: violated
counterexample path: t_load -> t_unload
final marking (real place ids): {'buf': 0, 'done': 1, 'raw': 2}
replay ok: True  json roundtrip ok: True

--- property: buf == 2 and done == 2 (impossible, total is 3) ---
verdict: safe

--- truncated search stays pending ---
verdict: pending  explored: 3

=== 2. Symmetry reduction vs. independent plain search ===
full states=4  reduced states=3  orbits=3  orbit sets match: True

=== 3. Incremental certificate revalidation ===
register: True
after widening t_load output -> revalidate: {'conservation': False}
after restoring t_load        -> revalidate: {'conservation': True}

=== 4. Finite capacity is NOT unbounded coverability ===
unbounded net could stack 2 tokens in p and fire 'escape';
with capacity 1, 'move' is disabled when p is full.
verdict for leak >= 1: safe  reachable markings: 2
```

## 测试结果

```
Ran 36 tests in 0.001s
OK
```

覆盖：容量/读弧/抑制弧启用语义、自环净效应判定、超容量禁用、精确状态数、
终态 token 总量不变量、可重放反例（含 JSON 往返、真实库所 ID）、截断保持
pending、证书接受/拒绝、增量重验（修改/删除/零效应编辑）、对称性验证与
归约对照、归约后反例重放、有限容量 ≠ coverability。
