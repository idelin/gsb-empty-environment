# BoundedQueue

有界阻塞 MPMC 队列（`include/bounded_queue.hpp`），元素在槽位上原地构造 /
析构，仅要求元素可移动构造 + 可移动赋值（支持 `std::unique_ptr` 等只移动
类型）。并发语义用互斥锁 + 条件变量实现。

## 异常保证

- `emplace` / `try_emplace`：元素构造函数抛异常时槽位**不被消费**
  （`size`/队尾不变），队列状态与存活对象计数均不变，槽位可立即复用。
- `wait_pop` / `try_pop`：向目标赋值（移动赋值）抛异常时，队首元素**留在
  槽位中**，后续 pop 仍可取到该元素。
- 队列析构时对所有仍存活的槽位对象逐一调用析构函数。

## 接口

- `emplace(args...)` 阻塞入队，关闭后返回 `false`
- `try_emplace(args...)` 非阻塞入队，满或关闭返回 `false`
- `wait_pop(out)` 阻塞出队，关闭且排空后返回 `false`
- `try_pop(out)` 非阻塞出队，空返回 `false`
- `close()` 唤醒所有等待者；`size()` / `capacity()`

## 构建与测试

```sh
cmake -S . -B build && cmake --build build
cd build && ctest --output-on-failure
```

测试（`tests/bounded_queue_tests.cpp`）使用带存活对象计数与故障注入的
只移动类型 `Counted`：注入构造异常验证槽位不泄漏，注入移动赋值异常验证
元素仍可取，并以 4 生产 / 4 消费压力测试核对总量与
`constructed == destroyed`、`live == 0`。
