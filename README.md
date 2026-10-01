# modelcheck — 层同步 BFS 有限状态模型检查器

纯 Python 3.11 标准库实现。对给定有限状态模型做 BFS 可达性探索并检查不变量，
返回最短违反轨迹；预算耗尽时返回 **未决（pending）** 及覆盖统计与未探索前沿证据。

## 设计要点

- **层同步 BFS + 去重屏障**：后继生成可并行（`workers>1`），但同一层的所有扩展
  完成后才同步做去重/入队决策，异步生成不会破坏按层最短性（`engine.py`）。
- **状态规范化保留恢复映射**：`normalize(state) -> (key, recovery)`，
  `recover(key, recovery) == state`；扩展与轨迹重建都使用恢复出的具体状态
  （`normalizers.py`，含排列对称约简 `PermutationNormalizer`）。
- **安全保持的状态压缩**：访问集按 64 位哈希分槽存储 zlib 压缩的完整状态；
  哈希碰撞时解压并比较完整原状态，绝不合并不同状态（`store.py`）。
- **分层磁盘前沿**：每层前沿独立落盘（原子替换），内存只持有当前层；
  引擎每轮从磁盘重读前沿，与恢复路径完全一致（`frontier.py`）。
- **Checkpoint 跨层恢复**：每层边界写入 store/parents/distances/前沿快照，
  `resume=True` 续跑不重不漏（`engine.py`）。
- **预算与未决**：`Budget(max_states, max_layers, max_seconds)` 按层粒度生效；
  耗尽即 PENDING（绝不报 safe），输出 `frontier_evidence.json`（未探索前沿 +
  覆盖统计），可从 checkpoint 续跑。
- **反例可重放**：`replay_trace` 用模型真实后继逐步重放轨迹并验证末态违反。
  初态违反返回长度 0 轨迹；不可达坏态不会成为反例。
- **独立对照**：`reference.py` 为不共享代码的朴素内存 BFS，测试用它对照
  引擎的可达集与每状态最短距离。

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 测试
python3.11 examples/run_demo.py               # 示例
```

## 目录

- `modelcheck/engine.py` — BFS 引擎、预算、checkpoint/恢复、轨迹重放
- `modelcheck/store.py` — 碰撞安全的压缩访问集
- `modelcheck/frontier.py` — 分层磁盘前沿
- `modelcheck/normalizers.py` — 恒等 / 排列规范化（含恢复映射）
- `modelcheck/encoding.py` — 确定性可逆状态编码
- `modelcheck/models.py` — 示例模型（互斥、对称计数器、显式迁移表）
- `modelcheck/reference.py` — 独立朴素 BFS 对照
- `tests/` — unittest 测试；`examples/run_demo.py` — 运行样例
