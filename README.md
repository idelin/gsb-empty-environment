# carpool — 拼车/取送规划器

在载重、座位与取送时间窗约束下，为最多 10 个订单规划车辆路线：
每个订单先取后送、全程不换乘（同一车辆完成取与送）。
目标为字典序：**先最少车辆，再最短总路程**（精确分支定界求解）。

## 构建与运行

```sh
go build -o carpool .
./carpool example.json          # 从文件读取
./carpool < example.json        # 或从标准输入读取
```

## 输入格式（JSON）

- `vehicle`: `weight_capacity`（载重上限）、`seat_capacity`（座位上限）
- `depot_departure`: 车辆从车场出发的时刻（缺省 0）
- `orders`: 最多 10 单，每单含
  - `id`、`weight`（货物重量）、`passengers`（乘客数）
  - `pickup_window` / `delivery_window`: `{earliest, latest}` 时间窗；
    早于 `earliest` 到达需等待，晚于 `latest` 到达则不可行
  - `pickup_service` / `delivery_service`: 装卸服务时长（缺省 0）
- `travel_time`: `(2n+1) x (2n+1)` 旅行时间矩阵。节点编号：
  `0` 为车场，`1..n` 为各订单取货点，`n+1..2n` 为对应送货点。
  路线距离含返回车场的一段。

## 输出格式（JSON）

- `vehicles` / `total_distance`: 最优车辆数与总路程；`optimal` 表示是否证明最优
- `routes[]`: 每辆车的 `distance` 与逐站 `stops`：
  - `node` / `order` / `kind`（`pickup` 或 `delivery`）
  - `arrival` / `wait`（等窗时长）/ `depart`
  - `load_weight` / `load_passengers`（离开该站后的车载载荷）
- `warnings`: 例如三角不等式不成立、或搜索达到上限

## 三角不等式与剪枝

求解器会检验矩阵是否满足三角不等式 `t[i][j] <= t[i][k] + t[k][j]`。
以下剪枝只在成立时才是成立的（sound），否则会被拒绝使用并在
`warnings` 中说明：

- **距离下界剪枝**：插入站点不再缩短路线（绕行非负），当前距离才可作为下界；
- **中间路线时间窗剪枝**：无三角不等式时，插入站点可能缩短行驶时间，
  使"当前超时"的路线重新可行，故此时中间路线只检查载重/座位，
  时间窗在完整解处统一校验；
- **"单车都不可行则订单不可行"的预检**：无三角不等式时绕行可能更快，预检跳过。

车辆数上界剪枝（不超过当前最优车辆数）与载重剪枝不依赖三角不等式，始终启用。

## 测试

```sh
go test ./...
```

包含与暴力枚举的随机对拍（度量与非度量矩阵）、时间窗/载重约束、
等待时间输出、三角不等式违例告警，以及 10 单性能测试。
