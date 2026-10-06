# 侵入式无锁队列 + 读侧保护 + 延迟回收

`intrusive_queue.hpp`：Michael-Scott 多生产者多消费者侵入队列，节点由调用者所有。

## 设计要点

- **调用者所有节点**：调用者分配 `Node` 派生对象并以引用入队；队列只自有一个哑元节点。
- **出队后仍可被观察**：出队返回的节点仍链接为哑元头，可能被并发线程遍历到；
  因此 `dequeue(Guard&)` 在摘除前先用冒险指针保护结果节点，调用者在 `Guard`
  复位/析构前可安全读取（显式读侧保护）。
- **延迟回收**：被后续出队摘除的旧头节点进入退役列表；`reclaim()` 仅在没有任何
  `Guard` 引用它时才调用调用者提供的 disposer 析构——节点只在无保护引用后析构。
- **关闭语义**：`close()` 后新 `enqueue` 返回 `false`；已入队节点可继续 `dequeue` 排空。

## 测试（test_intrusive_queue.cpp）

1. `test_close_and_drain`：关闭后入队失败、FIFO 排空、全部节点恰好析构一次。
2. `test_read_then_retire_interleaving`：用 `std::barrier` 确定性触发
   "A 持保护读 N1 → B 摘除并退役 N1 → B 回收被阻 → A 解除保护 → B 回收成功"，
   并在 disposer 中断言析构发生在保护解除之后。
3. `test_stress`：4 生产者 × 3 消费者 × 2000 节点，校验计数、求和与析构总数。

## 运行

```sh
./run_tests.sh   # 分别以 -fsanitize=address 和 -fsanitize=thread 构建并运行
```
