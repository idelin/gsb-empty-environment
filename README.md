# 乱序包重排器（离散事件模型）

纯 Go + 标准库实现。接收乱序到达的包，按 **(事件时间 tick, 序号 seq)**
释放；缺失包不永久阻塞，受**等待上限**与**缓存上限**双重约束；迟到包
进入单独旁路通道；已释放序列永不修改；`Reset` 用 epoch 清理旧队列且
不串流。全程为整数 tick 的离散事件模拟，**不发送任何真实网络流量**。

## 目录结构

- `reorder.go` / `reorder_test.go`：重排接收器核心包（包名 `reorder`）。
- `sim/sim.go`：整数 tick 离散事件调度器（最小堆，同 tick 按入队序）。
- `sim/link.go`：模拟链路——随机延迟（乱序）、丢包、重复，以及确定性
  脚本链路 `ScriptLink`，全部只在调度器上安排事件。
- `sim/sim_test.go`：调度器与链路测试，含固定种子的端到端集成。
- `cmd/demo/main.go`：确定性运行样例；输出存于 `sample_output.txt`。

## 核心语义

- **有序输出** `Out() <-chan Item`：`Item` 要么是释放的包，要么是
  `Gap{From, To, Tick, Reason}`。包严格按 `(Tick, Seq)` 交错投递；
  同一事件 tick 的包按序号先后释放。
- **旁路输出** `Side() <-chan SideItem`：
  - `late`：序号此前已被缺口越过，迟到副本不补回已释放序列；
  - `stale`：包属于已被 `Reset` 结束的旧 epoch，绝不串入新流；
  - `duplicate`：同一 `(epoch, seq)` 已到达过。
- **等待上限** `MaxWaitTicks`：缺口头部在 tick `t0` 形成，当
  `now - t0 >= MaxWaitTicks` 时把连续缺失段判为 `Gap(wait_timeout)`，
  随后级联释放；新缺口从当前 tick 重新起算。流空闲（没有后续包）时
  不凭空造缺口。
- **缓存上限** `CacheCap`：乱序缓存放满且又有乱序包到达时，跳过最旧
  缺失段（`cache_full`）并级联释放以腾位；跳过上界被夹在
  `到达包.Seq - 1`，绝不会把正在到达的包自身越过。
- **epoch 重置** `Reset(newEpoch, tick)`：清空缓存、缺口集合和等待
  状态，新流从 seq=1 重新开始；旧 epoch 残留包之后一律 `stale`。
- 事件时间 `Packet.Tick` 由发送方保证随序号单调非减；接收器时钟
  （`Handle`/`Tick` 的到达 tick）单调，倒流调用返回错误。

## 运行

```sh
go test ./...          # 全部测试
go test -race ./...    # 竞态检测
go run ./cmd/demo      # 运行样例
```

## 样例要点（见 sample_output.txt，MaxWait=4，CacheCap=3）

- seq 1..4 乱序到达，按事件时间与序号依次释放（1、2 同 tick 0）；
- seq 5 丢失，seq 6/7/8 填满缓存后，tick 10 因**缓存上限**跳过 seq 5；
- seq 10 丢失，其后续包已释放后在 tick 15 触发**等待超时**缺口；
- seq 1 的重复副本 tick 12 -> `duplicate`；seq 5 迟到副本 tick 16 -> `late`；
- tick 18 重置 epoch；旧 epoch 残留包 -> `stale`；新 epoch 的 3、1、2
  乱序到达后仍从 seq 1 起有序释放。
