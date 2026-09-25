# evcov — 证据覆盖计算引擎

纯标准库,Python 3.11。对带稳定 ID 与版本的事实(证据)、规则与计算步骤,
求满足区间、缺口、最长满足区间与最小证据子集,全部结果可重放。

## 语义

- 证据在 `[valid_from, valid_until)` 半开区间内有效,过期不能充数;撤回(retract)升版本并立即失效。
- 类别要求为"至少 k 个独立来源":同一来源的多条证据只计一源。
- 跨类别来源互斥:一个来源同一时间只能支持一个类别(联合二分匹配,不是各类别分别计数)。
- 端点扫描:相邻满足段并集合并,恰接边界(`[a,b)` 接 `[b,c)`)无长度缺口。
- 最小证据子集:小集合按大小穷举证明最小性;`2**n` 超预算时用贪心收缩给出可行见证,并将最优性标记为未证明,两者分开报告。
- 每个计算步骤记录稳定 ID、版本与输入/输出摘要(SHA-256),`replay()` 重算校验。

## 运行

```sh
python3.11 -m evcov.sample          # 合成样例
python3.11 -m unittest discover -s tests -v   # 测试
```

## 结构

- `evcov/model.py` — 版本化事实与规则(`Source` / `EvidenceItem` / `Rule`)
- `evcov/matching.py` — 联合来源-类别指派(Kuhn 增广路)
- `evcov/sweep.py` — 端点扫描:满足区间、缺口、最长区间
- `evcov/subset.py` — 带预算的最小证据子集搜索
- `evcov/replay.py` / `evcov/pipeline.py` — 稳定摘要、步骤日志与重放校验
- `evcov/sample.py` — 合成数据样例
- `tests/test_coverage.py` — unittest 测试
