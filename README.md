# masked_matmul

纯标准库(Python 3.11+)实现 **A×B 在结构掩码 M 下的增量维护**,不依赖 NumPy/SciPy。

## 特性

- **稀疏增删 + 批量事务**:A/B/M 的插入、删除可打包进一个事务,提交时原子发布。
- **贡献来源计数**:每条 (i, k, j) 路径单独记账 `(值, 计数)`,支持精确撤回与相互抵消——两条 k 路径相加为 0 时结果为空,撤回其一即恢复非零。
- **掩码补集不物化**:`ComplementMask` 只存排除项,10^7 维也只占 O(排除项) 内存。
- **热点行溢写**:行的中间贡献超过 `row_threshold` 时原子化溢写临时文件,常驻内存有界。
- **版本向量绑定**:查询必须携带完整 `(vA, vB, vM)`,未提交过的向量直接拒绝,无法跨事务拼接矩阵;已发布快照不可变,旧版本可并发读。
- **掩码删除只影响可见性**:贡献移入隐藏区继续维护,重新加入即完整恢复。
- **失败恢复**:事务取消、溢写失败(可注入)都会按日志回滚,已发布结果不受影响。
- **结果按行确定排序**:快照为按 (行, 列) 排序的不可变元组。
- **尺寸与索引显式输入**:越界立即报错;小规模可用 `dense_reference`(自写稠密三重循环)核验。

## 快速开始

```bash
python3 replay_sample.py     # 可重放样例(输出确定,自校验)
python3 -m unittest          # 运行全部测试
```

```python
from masked_matmul import Engine, ComplementMask

eng = Engine(10**6, 10**6, 10**6, ComplementMask(10**6, 10**6))
with eng.begin() as t:          # 批量事务
    t.a_insert(123, 456, 2)
    t.b_insert(456, 789, 3)
eng.query_latest()              # ((123, 789, 6),)
eng.query(eng.latest)           # 绑定版本向量查询
```

## 目录

- `masked_matmul/masks.py` — 显式/补集掩码
- `masked_matmul/state.py` — 带事件计数的稀疏矩阵(行+列索引)
- `masked_matmul/index.py` — 贡献索引、隐藏区、热点行溢写
- `masked_matmul/engine.py` — 事务、版本向量、快照发布与回滚
- `masked_matmul/reference.py` — 自写稠密参考实现
- `replay_sample.py` — 可重放样例
- `tests/test_engine.py` — unittest 测试
