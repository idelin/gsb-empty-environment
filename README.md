# stabber — 闭整数区间刺点求解器

给定一组闭整数区间 `[lo, hi]` 和一组带非负成本的候选点，选择候选点的子集，
使每个区间都至少被一个选中点刺中。候选点全局共享：一个点可以同时刺中
多个区间，因此**不能**逐区间独立选最便宜的点（见 `TestSharedCoverageBeatsIndependent`）。

## 目标序（字典序优先级）

1. 总成本最小；
2. 点数最少；
3. 选中点升序列表字典序最小。

## 用法

```sh
go build -o stabber .
./stabber -in examples/sample.json          # 文件输入，- 表示 stdin
cat examples/sample.json | ./stabber
```

参数：

- `-verify`（默认开）：用候选点有限子集枚举（2^m 个子集）独立核对结果；
- `-max-enum N`（默认 22）：候选点数超过 N 时跳过枚举核对；
- `-node-limit N`：分支定界节点上限，触发后输出中断界；
- `-time-limit MS`：墙上时间上限（毫秒）。

## 输入格式

```json
{
  "intervals": [[1, 3], [2, 4]],
  "candidates": [{"point": 2, "cost": 10}, {"point": 1, "cost": 6}]
}
```

重复候选点按最低成本合并；刺不中任何区间的候选点被丢弃（计入
`candidatesDropped`）。负成本或 `lo > hi` 报错。

## 输出

- `status`: `optimal` | `interrupted` | `infeasible`；
- `points` / `cost`：选中点（升序）与总成本；
- `proof`：**逐区间被刺中的证明**——每个输入区间给出一个刺中它的选中点；
- `infeasibleIntervals`：**不可行证据**——不含任何候选点的区间列表；
- `bounds`：成本下界/上界。`optimal` 时两者相等；`interrupted` 时
  `lower <= 最优值 <= upper`（下界来自搜索栈上未探索子树的界，
  上界为当前最优可行解）；
- `verification`：枚举核对结果（子集数、是否一致、枚举最优值）。

## 方法

- 分支定界：按右端点排序区间，选候选数最少的未覆盖区间分支；
  下界 = 当前成本 + 未覆盖区间中一组两两不相交区间的最小候选成本之和
  （不相交区间无法共享刺点，各需一个独立点）；点数下界同理用于剪枝。
  仅在 (成本, 点数) 严格劣于 incumbent 时剪枝，同成本同点数继续搜索
  以保证字典序最优。
- 初始 incumbent 由贪心给出（最小右端点区间 + 最便宜/覆盖最多候选点）。
- 中断时下界 = min(所有未探索祖先节点的下界, incumbent)，始终有效。
- 枚举核对器与求解器共享同一目标序，随机化交叉测试见
  `solver_test.go` 的 `TestRandomizedCrossCheck`。

## 测试

```sh
go test ./...
```
