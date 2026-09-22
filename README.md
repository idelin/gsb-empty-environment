# dynamic-scc — 可更新的强连通分量索引

纯 Python 3.11（仅标准库），不依赖图数据库。维护一个有向**多重图**
（允许平行边与孤立点）的强连通分量划分与凝聚图，支持原子批量更新、
固定版本查询和差量输出。

## 接口

```python
from dynamic_scc import DynamicSCC, reference_index, apply_diff

idx = DynamicSCC()
diff = idx.apply_batch([("add_edge", "A", "B"), ("add_edge", "B", "C")])
diff = idx.apply_batch([("add_edge", "C", "A")])   # 三点合并为分量 "A"
diff.merges          # [("A", ("A", "B", "C"))]
diff.cond_edges_removed  # {("A", "B"): 1, ("B", "C"): 1}

idx.components()               # {分量ID: frozenset(成员)}，ID = 最小成员字符串
idx.condensation_edges()       # {(源分量, 目标分量): 重数}
idx.component_of("B")          # "A"
idx.components(version=1)      # 固定历史版本查询
idx.version                    # 当前版本号（每次成功提交 +1）
```

操作类型：`("add_node", n)`、`("remove_node", n)`、`("add_edge", u, v)`、
`("remove_edge", u, v)`。`add_edge` 自动创建缺失端点；`remove_node`
连带删除所有关联边（多重边逐条计数，删一条平行边不断开分量）。

## 原子性

批次先整体校验（边/点存在性、操作合法性），任一操作非法则整批抛
`ValueError`，图与版本号零副作用——无需回滚，因为校验通过前不写入。

## 差量（BatchDiff）

- `added_components` / `removed_components`：可重放的分量增删（ID → 成员）；
- `merges` / `splits` / `formed` / `dissolved`：由增删推导的合并、拆分、
  新生、消亡事件；
- `cond_edges_added` / `cond_edges_removed` / `cond_edges_count_changed`：
  凝聚图边的新增、删除与重数变化。

`apply_diff(comps, cond, diff)` 可将差量流重放到任意消费端；
`test_dynamic_scc.py` 中的随机测试验证「增量索引 == 全量参考 ==
差量重放」三方一致。

## 维护策略与最坏情况重算范围

每批更新在**旧图**上从所有被触碰的端点做正/反向可达遍历，得到受影响
区域 R（这些点的旧分量必整体落入 R，因为分量成员互相可达）。然后：

1. 仅对 R 的诱导子图跑迭代版 Tarjan，O(|V_R| + |E_R|)；
2. 与 R 不相交的分量原样保留——分量 ID 只取决于成员集合，因此无关
   分量的身份对遍历顺序和其他区域的修改绝对稳定；
3. 凝聚图只重扫 R 节点的出/入边，两端都在未受影响分量中的边不动。

**最坏情况**：单条边的增删即可改变全图连通性（例如长链成环/断环），
此时 R = V，单批退化为 O(V + E) 全量重算——这与全动态 SCC 的理论下界
一致，无增量算法能在该情形下保证更优。实际收益取决于变更的局部性：
改动越局部，R 越小。凝聚图重建为 O(R 的关联边数)。

**版本存储**：每个版本保存一份快照（分量、节点→分量、凝聚边），内存
O(版本数 × (V + E))。frozenset 不可变，未变分量跨版本共享，实际开销
低于上界；如需长历史可改为只存差量并惰性回放。

## 参考实现与测试

`reference_index(nodes, edges)` 是全量 Tarjan 参考实现，用于小规模
对拍。运行测试：

```
python3 -m unittest -v
```

覆盖：A→B→C 加 C→A 合并、删 B→C 拆分；无关分量身份稳定；平行边删一
条不断开；节点删除清理关联边与分量证据；无效批次回滚；旧版本查询；
400 批随机操作与全量参考及差量重放的三方对拍。
