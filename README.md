# 拼车规划器 (Carpool Planner)

对最多 10 个订单求精确最优派车方案:**第一目标最少车辆数,第二目标最短总路程**。
每单包含一个取货点和一个送货点,约束如下:

- 车辆同质,同时受**载重**与**座位数**限制(容量按车上瞬时载荷计算);
- 每个订单有取/送**时间窗**:到达晚于窗口关闭则不可行,早于窗口开启则需等待;
- **先取后送**,且同一订单的取送必须由同一辆车完成(**禁止中途换车**);
- 车辆从车场(depot)时刻 0 出发,最终返回车场。

## 用法

```sh
go run . example.json      # 从文件读取
cat example.json | go run .  # 或从标准输入读取
go test ./...              # 运行测试
```

## 输入格式 (JSON)

```json
{
  "depot": 0,
  "vehicle": {"weight_capacity": 50, "seat_capacity": 2},
  "orders": [
    {"id": "A", "pickup": 1, "delivery": 2, "weight": 10, "seats": 1,
     "pickup_window": {"open": 0, "close": 60},
     "delivery_window": {"open": 0, "close": 200},
     "service_time": 5}
  ],
  "travel_time": [[0, 10, 20], [10, 0, 10], [20, 10, 0]]
}
```

- `travel_time[i][j]`:节点 i 到 j 的旅行时间,必须非负且**满足三角不等式**;
- 节点编号 0..N-1,`depot` 为车场;订单数 ≤ 10,超出即报错;
- 到达时间 ≤ 窗口 `close` 才可行;`departure = max(arrival, open) + service_time`。

## 输出格式 (JSON)

```json
{
  "vehicles": 1,
  "total_travel_time": 84,
  "routes": [
    {"vehicle": 1, "orders": ["A"], "travel_time": 84,
     "stops": [
       {"node": 0, "action": "depart-depot", "arrival": 0, "wait": 0, ...},
       {"node": 1, "action": "pickup", "order": "A", "arrival": 10,
        "wait": 20, "departure": 35, "load_weight": 10, "load_seats": 1,
        "leg_travel": 10},
       {"node": 0, "action": "return-depot", "arrival": 114, ...}
     ]}
  ]
}
```

逐站输出:到达时刻、`wait` 等待时间、出发时刻、离站后的车上
`load_weight`/`load_seats` 载荷、以及上一段路程 `leg_travel`。

## 三角不等式校验

求解前校验 `t[i][j] <= t[i][k] + t[k][j]` 对所有 `(i,j,k)` 成立,否则直接
拒绝输入。原因:分支定界依赖两个只在三角不等式下成立的剪枝——

1. **最早到达剪枝**:从当前位置到任一未服务站点的最早到达时间为
   `now + t[cur][next]`(任何绕行都不会更快),若已晚于其窗口关闭则剪枝;
2. **组代价单调性**:向一个车辆组加入订单不会降低该组最优路程
   (超集路线删站即得子集可行路线),保证部分划分的代价下界单调。

## 算法

精确分支定界,分两层:

1. **划分层** (`assign`):枚举订单到车辆的集合划分(带对称性剪枝),
   按字典序 `(车辆数, 总路程)` 维护 incumbent 并剪枝;组代价按位掩码缓存;
2. **路径层** (`routeDFS`):对每组订单枚举取送站序列(先取后送、容量、
   时间窗),用"每个未访问站点 + 车场至少各需一条入边"的下界与最早到达
   检查剪枝,求该组最优路线。

复杂度对 n=10 可接受:组代价缓存至多 2^10 项,路径搜索被容量/时间窗
与下界强力剪枝。
