# minigrad — 纯标准库微型自动微分引擎

Python 3.11，仅依赖标准库，CPU 小规模张量自行实现（flat `list[float]` + 行主序 strides），
不使用任何自动微分或张量库。测试基于 `unittest`。

## 设计要点

- **反向按拓扑累计**：`Graph.backward` 从输出做 DFS 得到拓扑序，逆序执行节点；
  扇出张量的多条梯度贡献先累加再下传，共享节点每次反向只执行一次反向逻辑。
- **保留图 / 释放图**：`backward(..., retain_graph=True)` 保留图可重复反向并累加梯度；
  默认反向成功后消费并释放图，第二次 `backward` 抛出 `GraphReleasedError`；
  `release()` 不可重入，并发释放只有一个赢家。
- **原地修改版本计数**：每个 `Tensor` 持有 `_version`，`add_`/`mul_`/`fill_`/`zero_` 等
  原地操作使其递增。节点在前向时记录被保存输入的版本，反向时校验，
  不一致抛出 `StaleInputError`。
- **原子梯度事务**：反向分两阶段——先算入私有缓冲，全部成功后一次性提交到叶子
  `.grad`。中间算子异常（可用 `ops.apply` 注入）或 `CancelToken` 取消都会回滚，
  已提交梯度不被污染；各 `Graph` 完全独立，一个图的失败不影响其他图。
- **重算节点**：`ops.dropout(x, p, key)` 不保存掩码，反向时按绑定的随机键
  确定性重算，同时仍校验输入版本。
- **存储生命周期**：`Storage` 引用计数（pin）。图保存的张量会被 pin；
  释放图只 unpin 自己持有的部分，仍被其他图引用的存储不可回收。
- **数值核验**：测试中用中心差分 `(f(x+h)-f(x-h))/2h` 独立验证复合函数的梯度。

## 目录

```
minigrad/    tensor.py 存储与原地操作 | ops.py 算子 | graph.py 图与反向引擎 | errors.py
tests/       test_minigrad.py  18 个用例
examples/    demo.py           七段运行样例
```

## 运行

```bash
python3.11 -m unittest discover -s tests -v
python3.11 examples/demo.py
```

## 测试结果

```
Ran 18 tests in ~0.8s
OK
```

覆盖：拓扑累计与扇出、共享节点单次执行、保留/释放/二次反向拒绝、版本计数检测、
原子事务回滚（注入异常 + 取消）、多图共享参数累加、独立图隔离、重算节点
（版本 + 随机键）、存储 pin 生命周期、并发释放唯一赢家、释放与反向的锁互斥、
两组有限差分数值核验。
