# bitemp-index：双时态版本索引

每条记录同时携带两条时间轴：

| 轴 | 字段 | 含义 |
| --- | --- | --- |
| 业务时间（valid time） | `[valid_from, valid_to)` | 事件真实发生的区间，左闭右开 |
| 录入时间（transaction time） | `[tx_from, tx_to)` | 证据在系统中生效的区间，左闭右开 |

查询输入为 **(业务时间 `valid_time`, 观察时刻 `as_of`)**，只能看到
`tx_from <= as_of < tx_to` 的版本——即观察时刻之前（含当刻）已录入、
且尚未被更正/撤销的证据。两类查询：

- 「10 时发生了什么」：`store.query(obj, valid_time=10, as_of=τ)`，固定业务时间、移动观察时刻；
- 「当时我们知道什么」：`store.snapshot(obj, as_of=τ)`，固定观察时刻、展开全部可见版本。

## 写入语义（历史永不丢失）

- `assert_version(...)`：录入新证据。业务区间可在过去（补录）或未来（未来版本）。
- `correct(record_id, ...)`：把旧版本的 `tx_to` 关闭为 `at`，追加继承其字段的新版本。
- `retract(record_id, at=...)`：只关闭 `tx_to`，不留替代版本（追溯撤销）。
- 旧版本一律保留在库里（`store.history(obj)` 可见全量双时态历史），
  因此任何历史 `as_of` 的查询结果永远不被后续写入污染。

主场景：事件 10 在 20 录入为 A、30 更正为 B —— `as_of=25` 查询得 A，`as_of=35` 得 B。

## 重叠区间的优先规则（全序，与到达/遍历顺序无关）

同对象、同业务时刻、同观察时刻出现多条可见版本时：

1. `priority` 大者优先（业务显式声明，如“审计值压过抓取值”）；
2. `tx_from` 晚者优先（同级证据里后录入的信息更新）；
3. `record_id` 字典序小者优先（`record_id` 全局唯一，构成确定性兜底）。

三条键合起来是全序：任意插入顺序都得到同一裁决结果
（`tests/test_core.py::test_result_independent_of_insertion_order` 验证）。

## 索引结构

每对象一个 `_ObjectIndex`：`record_id -> Version` 字典 + 按
`(valid_from, valid_to, record_id)` 排序的键列表。点查询先二分定位
`valid_from <= t` 的前缀，再按 `valid_to > t` 与 tx 窗口过滤；
快照查询按 tx 窗口过滤后排序输出。关闭版本只改 `tx_to`，排序键不变，索引无需维护。

## 参考实现与随机测试

`bitemp/reference.py` 是不依赖任何索引的全表扫描实现，裁决规则独立重写。
`tests/test_property.py` 对每个 seed 生成随机操作序列（录入/更正/撤销/补录/
未来版本），同时作用于两个实现，再用随机 `(valid_time, as_of)` 逐点比对
`query` / `candidates` / `snapshot`，并验证 GC 后 horizon 之后的查询不变。

## 旧版本保留与回收条件

- **保留**：默认全部保留。任何 `tx_to == INF` 的当前版本永不回收；
  已关闭版本是重放历史 `as_of` 查询与审计的唯一依据。
- **回收**：`store.gc(horizon)` 物理删除 `tx_to <= horizon` 的版本。
  安全条件是 `horizon` 不晚于系统承诺服务的最早观察时刻：tx 区间左闭右开，
  `tx_to <= horizon <= as_of` 的版本对任何 `as_of >= horizon` 的查询本就不可见，
  回收不改变这类查询的结果；`as_of < horizon` 的旧查询在回收后不再保证可重放。
  若业务有审计保留期，应令 `horizon <= now - 保留期`。

## 运行

```bash
python -m pytest          # 单元测试 + 随机序列对照测试
python -m bitemp.demo     # 两种时间轴的查询演示
```

要求 Python >= 3.11，无第三方运行时依赖。
