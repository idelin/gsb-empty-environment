# 跨源置信证据决策：可追溯规则推理引擎

纯 Python 3.11+ 标准库实现，无第三方依赖。所有示例数据均为合成数据。

## 语义模型

- **规则**：有限前提事实集合 → 单一结论事实；不允许否定（单调推理）。
- **来源**：事实可被多个相互独立的来源直接断言（基事实）。
- **推理**：从基事实出发的最小不动点前向链。只有能追溯到真实来源的
  事实才成立；循环规则（如 `A→B`、`B→A`）在没有外部来源支撑时不会
  凭空成立。
- **可追溯性**：每个事实附带全部"按集合包含极小"的来源集合
  （极小集合构成的反链）。输出排序：先按来源数量升序，再按字典序。
- **撤销**：`revoke` 只删除该来源自身的断言并重算不动点；
  失去全部支持的派生事实随之消失，仍有其他来源支持的事实保留。
- **预算中断**：来源集合数量可能指数增长。`budget` 限制工作量步数，
  超预算立即中断，输出 `truncated: true`（此时结果为部分结果，不保证完整）。

## 运行

```sh
python3 reasoner.py 输入.json            # 从文件读取
cat 输入.json | python3 reasoner.py      # 从标准输入读取
python3 reasoner.py 输入.json --budget 1000 --compact
python3 -m unittest -v                   # 运行测试
```

## 输入格式（JSON）

```json
{
  "sources":    {"来源名": ["事实", ...], ...},
  "rules":      [{"premises": ["前提事实", ...], "conclusion": "结论事实"}, ...],
  "operations": [{"op": "assert", "source": "来源名", "facts": ["事实", ...]},
                 {"op": "revoke", "source": "来源名"}],
  "budget":     100000
}
```

- `sources`、`rules`、`operations`、`budget` 均可选，缺省为空 / 无预算。
- `operations` 在 `sources` 与 `rules` 加载后按顺序执行，用于模拟
  断言与撤销；最终结果在全部操作之后计算一次。
- 空 `premises` 的规则表示公理，其结论由空来源集合 `[]` 支持。
- 命令行 `--budget` 覆盖输入中的 `budget`。

## 输出格式（JSON）

```json
{
  "facts":     {"事实": [["来源", ...], ...], ...},
  "truncated": false,
  "steps":     42,
  "budget":    100000
}
```

- `facts`：每个成立事实映射到其全部极小来源集合；集合内部来源按
  字典序排列，集合之间先按来源数量再按字典序排列。不成立的事实不出现。
- `truncated`：`true` 表示因预算中断，结果为部分结果。
- `steps`：实际消耗的工作量步数；`budget` 为生效的预算（`null` 表示无限制）。

## 验收场景

```sh
python3 reasoner.py examples/cycle_revoke.json
# => {"facts": {}}            撤销 s 后 A、B 都消失

python3 reasoner.py examples/cycle_with_independent_source.json
# => A、B 均由 ["t"] 支持      独立来源 t 给 B 后，撤销 s 仍保留二者

python3 reasoner.py examples/exponential.json
# => Y 有 16 (=2^4) 个极小来源集合
```

## 文件

- `reasoner.py` — 引擎与命令行入口
- `test_reasoner.py` — unittest 测试（16 个用例）
- `examples/` — 合成示例数据
