# 无锁安全队列ABA见证

告警队列的比较交换可能因节点地址复用误删另一条告警。写供Go模型宿主调用的执行包，输入至多三线程的读指针、分配、释放与CAS脚本，地址池只有三个槽，每次分配产生新代次；同时计算裸地址CAS和带代次CAS结果，找两者首次分歧并造成事件丢失的最短交错。用完整状态枚举核对，证据必须展示实际丢失的事件ID。

## 模型

- 地址池固定 3 个槽，每次 `alloc` 使槽代次递增并写入事件 ID。
- 每线程脚本由 `read` / `next` / `alloc` / `free` / `cas` / `setnext` 组成，线程间全部交错均可枚举。
- 裸地址 CAS 只比较槽地址（ABA 下误成功）；带代次 CAS 同时比较槽地址与代次。
- 事件丢失：分歧步后某事件在裸地址语义下从头指针不可达、而带代次语义下仍可达。

## API

- `FindShortestLoss(scripts)`：BFS 找首次分歧且丢事件的最短交错，返回 `Witness`（含逐步两种 CAS 结果与丢失事件 ID）。
- `EnumerateAll(scripts, maxStates)`：完整状态枚举，核对见证最短、统计全部丢事件交错与反向分歧。
- `RunSchedule(scripts, sched)`：按给定线程号序列同时重放两种语义，逐步返回 CAS 结果。
- `ParseScript(text)`：解析文本脚本（`T0 read h`、`T1 alloc x 7`、`T0 cas h n` 等）。

## 运行

```sh
go test ./...        # 含完整状态枚举核对
go run ./cmd/aba-demo  # 打印经典 ABA 场景的最短丢失交错证据
```
