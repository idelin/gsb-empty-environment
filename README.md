# dynflow — 有向多重图整数最大流的增量修复

纯 Python 3.11 标准库实现（`unittest` 测试）。在有向多重图上维护整数最大流，
支持**原子批量更新**（容量增减、边停用、节点供需变化），更新在**既有流的残余
网络**上就地修复，绝不清零重算，并统计访问的残余边数。

## 特性

- 顶点、边均为字符串 ID；**平行边（多重边）与孤立点完整保留**，零容量边合法。
- 节点供需：`balances[v] > 0` 供应、`< 0` 需求、`= 0` 转运；内部以超级源/汇
  （S*/T*）终端弧建模。
- 每次 `apply_update` 原子提交：先在克隆状态上执行，失败（参数非法，或
  `strict=True` 时供需不可满足）则抛异常且**原快照完全不变**。
- 容量缩小/停用先**残余改道**（reroute，可使用反向残余弧），不足部分**成对
  反向撤回**（cancel），任何中间状态都不会留下超容量边；随后在残余网络上
  继续增广恢复最大性。
- 供需不可满足默认不是错误：提交最大可行流，报告中给出
  `total_supply / total_demand / satisfied` 缺口信息；`strict=True` 可改为
  拒绝并回滚。
- 每次更新返回 `UpdateReport`：新流、**最小割证书**（源侧顶点 + 跨割弧 +
  割容量 = 流值）、逐条**变化原因**（`Change`）、修复/增广阶段各自访问的
  残余边数。
- 历史快照只读，版本时间区间**左闭右开** `[t_k, t_{k+1})`（最新版本右端
  `None` 表示 +∞），`hist.at(t)` 读取任意时间点生效的旧快照，
  `FlowNetwork.from_snapshot(snap)` 可重建独立网络（流一并恢复）。

## 快速开始

```python
import dynflow

net = dynflow.FlowNetwork(
    nodes=["s", "t"],
    edges=[("e1", "s", "t", 2), ("e2", "s", "t", 3)],  # 平行边
    balances={"s": 5, "t": -5})
hist = dynflow.FlowHistory(net, initial_time=0)
assert hist.latest.value == 5                            # 最大流 5

rep = hist.apply_update(1, capacity={"e2": 1})           # 3 -> 1
assert rep.value == 3 and rep.flow == {"e1": 2, "e2": 1}
assert rep.cut.capacity == 3                             # 割证书与流值一致
print(rep.explain())                                     # 变化原因 + 访问统计

assert hist.at(0).value == 5 and hist.at(1).value == 3   # 读旧快照
```

批量更新一次原子提交：

```python
rep = hist.apply_update(
    2,
    capacity={"e1": 4},        # 容量增/减
    deactivate=("e2",),        # 停用（不可逆，名义容量保留，流强制为 0）
    balances={"s": 4, "t": -4} # 节点供需变化
)
```

## 运行

```bash
python3.11 example.py     # 可运行样例（平行边/菱形改道/批量/不可满足/快照）
python3.11 -m unittest test_dynflow -v
```

## 测试策略

- **题面样例**：平行 S→T 容量 2、3 最大流 5；3 降为 1 后流值变 3。
- **菱形反向改道**：`ab` 2→1 后唯一改道路径 `a→s→c→b` 必须使用 `s→a` 的
  反向残余弧，流值保持 2；对照组（无支路）只能撤回、流值下降。
- **零容量边 / 多重边 / 孤立点 / 自环**：保留且行为正确。
- **不可满足供需**：默认尽力流 + 缺口报告；`strict` 抛 `InfeasibleFlowError`
  且快照不变。
- **原子性**：7 类非法批量（未知 ID、负容量、非整数、重复停用、同时改+停、
  时间不递增……）逐一验证抛错且快照不变。
- **独立校验器**（不复用实现内部代码）：容量约束、流守恒（含终端弧）、
  残余可达性重算、割容量独立合计。
- **穷举小割交叉检验**：枚举原始顶点全部 2^n 子集求最小割，与流值比较；
  固定种子的 50 轮随机批更新（含随机非法请求）每轮全量校验，所有历史
  版本快照同样逐一校验。
- **增量性**：小改动只触达局部（改变的边数少、访问残余边数显著小于同状态
  清零重算），空更新零修复访问且流不变。

## 文件

- `dynflow.py` — 实现（`FlowNetwork` 网络与残余结构，`FlowHistory` 原子
  批处理与快照历史）。
- `test_dynflow.py` — `unittest` 测试（含独立校验器与穷举割交叉检验）。
- `example.py` — 可运行样例。
