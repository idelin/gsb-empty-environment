# RCPSP 求解器（Go）

离散时域、不可抢占的资源约束项目调度求解器：最多 12 项作业，整数时长（可为 0）、
前置关系、多资源需求。求**最短完工期（makespan）**，同优时取**开始时间向量字典序最小**
（按作业下标顺序）。零时长作业不占用时间与资源，但前置约束照常生效。

## 输入（JSON，stdin 或 `-f` 文件）

```json
{
  "horizon": 12,
  "resources": [{"name": "crew", "capacity": 3}],
  "jobs": [
    {"duration": 3, "demands": [2], "predecessors": []},
    {"duration": 0, "demands": [0], "predecessors": [0]}
  ]
}
```

- `horizon`：离散时域 `[0, horizon)`，所有作业须满足 `start + duration <= horizon`
- `demands[r]`：作业执行期间占用资源 r 的数量；`predecessors` 为作业下标列表

## 输出

- `status`: `optimal` / `infeasible`（附 `reason`：环、需求超容量、时域内无解）
- `makespan`、`starts`：最优完工期与字典序最小开始时间向量
- `timeline`：逐时资源证据（每时槽的资源用量与在制作业集合）

## 用法

```sh
go build -o rcpsp .
./rcpsp -f example.json            # 求解
./rcpsp -f example.json -verify    # 同时跑全枚举暴力核对（仅限小规模）
go test ./...                      # 单元测试 + 400 组随机对拍
```

## 方法

- 前置 DAG 最长路得最早开始时间（同时检测环）；按完工期上界 T 反推最晚开始时间
- T 从下界（前置链长、资源负载）递增，首个可行 T 即最优
- 固定 T 后按作业下标顺序 DFS、开始时间升序尝试，首个完整解即字典序最小；
  用逐时资源用量数组做增量可行性检查与前向检查剪枝
- `brute.go` 为独立的全枚举实现，测试中与主求解器对拍（含不可行与多资源冲突场景）
