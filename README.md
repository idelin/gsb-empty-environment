# secxpl — 投影优先的安全结论解释 + 有限域成对非干扰测试器

纯 Python 标准库实现（兼容 3.11+），测试使用 `unittest`。所有样例数据均为合成数据
（虚构的孵化器遥测场景，见 `secxpl/sample.py`）。

## 威胁模型与低可见输入等价关系

- 每个事实带敏感度级别 `level`，调用方持有许可级别 `clearance`；
  **投影** `project(world, clearance)` 只保留 `level <= clearance` 的事实。
- **低可见输入等价**：两套完整事实集在某一许可下等价，当且仅当它们的投影相同
  （即仅隐藏部分不同）。等价定义见 `secxpl/noninterference.py` 的 `low_equivalent`。
- **安全不变式**：公开输出的每一个字节——解释树、错误串、结构计数、调试日志——
  都是 `f(投影, 公开规则集, 查询)` 的纯函数。因此低可见等价的输入必然产生完全相同的
  公开解释、错误和结构计数；隐藏事实的数量既不会从路径名也不会从计数/路径长度泄漏。
- **明确不承诺**：运行时间（计时）侧信道不在保证范围内。

## 设计要点

- **投影优先**：推导引擎只接收投影后的事实集（`secxpl/explain.py`），而非先全量推导再删字段。
- **受限占位**：结论可见但部分前提不可见时，输出规范化的"潜在支持"骨架，所有不可见前提
  折叠为**单个** `<restricted-support>` 占位节点，并显式标记 `status: incomplete`
  （解释不完整）。一个占位符代表整段隐藏支持，链长与计数永不反映隐藏数量。
- **错误不可区分**：隐藏事实与不存在的事实返回同一通用错误 `ERROR[not-visible]`，
  不泄漏事实存在性。
- **稳定 ID 与版本**：事实、规则、步骤均带稳定 ID 与版本；步骤 ID 由
  `sha256(rule@version | premise@version ...)` 派生，跨运行稳定。
- **证据可重放**：每条解释附带 `Evidence`（投影事实 + 公开规则 + 转录哈希），
  `replay(evidence)` 可独立重推导并校验。
- **缓存隔离**：`ExplanationCache` 按许可级别分命名空间，不同权限之间绝不共享条目。
- **调试信息同规则**：调试日志由投影后的推导过程产生，只出现可见事实 ID 与可见计数。

## 反例：朴素删字段仍会泄漏

`secxpl/naive.py` 实现了"先全量推导、再删除受限字段"的朴素方案。它保留了全量树的
`steps_total`、`depth`、`deleted` 计数和全量调试轨迹，因此两个低可见等价世界
（隐藏支持链长度 1 vs 2）产生不同输出——非干扰测试器在 32 个世界的有限域上检出
224 处违规，而修正后的投影优先渲染器为 0 处。

## 运行

```bash
python3 -m secxpl.demo                          # 运行样例
python3 -m unittest discover -s tests -v        # 运行测试
```

## 运行样例（节选 `python3 -m secxpl.demo`）

```
=== 1. clearance 0, query 'shutdown-required' (restricted premises) ===
query: shutdown-required
status: incomplete
tree:
  - potential-support via rule R2-shutdown@1 (restricted premises omitted)
    - step S-R1-nominal-821b70636866 v1 (rule R1-nominal@1) -> system-nominal
      - fact temp-reading-ok@1 "synthetic: 36.9C nominal"
      - fact power-ok@1 "synthetic: bus voltage nominal"
    - <restricted-support>
counts: {"depth": 3, "facts": 2, "placeholders": 1, "steps": 2}

=== 3. hidden fact vs nonexistent id: identical errors ===
hidden:      ERROR[not-visible]: no public explanation available for 'manual-override'
nonexistent: ERROR[not-visible]: no public explanation available for 'nonexistent-id'
identical up to the caller-supplied id: True

=== 4. low-equivalent pair, naive renderer LEAKS via counts/depth ===
world A (hidden chain length 1):  counts: {"deleted": 2, "depth": 2, "steps_total": 2}
world B (hidden chain length 2):  counts: {"deleted": 3, "depth": 2, "steps_total": 3}

=== 5. same pair, secure renderer: identical transcripts ===
identical: True

=== 6. evidence replay ===
replay(evidence): True

=== 7. finite-domain pairwise noninterference test (32 worlds, 8 queries, clearance 0) ===
naive renderer violations:  224
secure renderer violations: 0
```

## 测试结果（`python3 -m unittest discover -s tests -v`）

```
Ran 16 tests in 0.563s
OK
```

覆盖：完整/不完整解释与占位、计数不含隐藏统计、错误不可区分、低可见等价关系、
链长不泄漏隐藏数量、朴素实现泄漏（反例）、有限域非干扰（安全 0 违规 / 朴素被检出）、
公开输出不含隐藏 ID、调试遵循投影、缓存权限隔离、证据重放与篡改检测、ID/版本稳定性。

## 目录结构

- `secxpl/model.py` — 事实/规则/步骤/世界，均带稳定 ID 与版本
- `secxpl/engine.py` — 确定性前向链接推导引擎（纯函数）
- `secxpl/projection.py` — 可见性投影与投影摘要
- `secxpl/explain.py` — 投影优先的安全渲染器、受限占位、证据与重放
- `secxpl/naive.py` — 不安全的朴素删字段渲染器（反例，仅用于演示泄漏）
- `secxpl/noninterference.py` — 低可见等价关系与有限域成对非干扰测试器
- `secxpl/cache.py` — 按权限隔离的解释缓存
- `secxpl/sample.py` — 合成样例数据与有限测试域
- `secxpl/demo.py` — 可运行样例
- `tests/test_secxpl.py` — 16 个 unittest 用例
