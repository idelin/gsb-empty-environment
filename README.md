# index_reclaim

一个面向 C++17 索引宿主的回收许可组件。组件只处理合成对象标识
`(address, generation)`、根发布序号、线程令牌和保护槽，不解引用任何真实地址。

## 构建

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

宿主导入：

```cmake
add_subdirectory(path/to/index_reclaim)
target_link_libraries(your_index PRIVATE index_reclaim)
```

公共头文件：

- `include/index_reclaim/permission.hpp`
- `include/index_reclaim/reference.hpp`

## 阅读协议

每次读取使用三步：

```cpp
auto first = authority.read_root();
auto attempt = authority.publish_protection(thread, first);
auto guard = authority.finish_protection(attempt, authority.read_root());
if (guard.valid()) {
    // 此时地址与代次均相同，且槽版本仍是 attempt 发布后的版本。
    // 只在 guard 存活期间使用该对象。
}
```

`begin_read()` / `finish_read()` 是读取 `Authority` 自身合成根的便捷形式。
真实宿主可将自己的原子根快照传给 `publish_protection()` /
`finish_protection()`，但第二次快照必须发生在保护发布之后。

槽使用奇偶版本号保护 `(occupied, address, generation)` 元组。`ReadGuard::valid()`
会重新核对线程实例和槽版本，因此同一令牌后续覆盖槽后，旧 guard 自动失效。
失败路径会尝试清空仍属于本次发布的槽；如果槽已被覆盖，则不会误清别人的保护。

对象标识从不只依赖地址。地址复用必须配新的、非回绕的代次；
`GenerationSequence::next()` 在耗尽代次时抛出 `std::overflow_error`。

## 退休与许可

根发布序号单调递增。宿主先从根移除对象，再：

```cpp
auto removed = authority.publish_root(new_root);
BatchId batch = authority.retire(old_object, removed.publication);
```

回收器分片扫描：

```cpp
auto scan = authority.begin_scan();
for (std::size_t shard = 0; shard < authority.shard_count(); ++shard) {
    authority.scan_shard(scan, shard);
}
auto result = authority.claim(scan);
for (auto& permit : result.permits) {
    if (permit.consume()) {
        // 一次性许可，可以移交给实际释放逻辑。
    }
}
```

每个分片证据记录：

- 分片注册版本；
- 该分片内每个存活线程的 `(thread_index, incarnation, slot_version)`；
- 扫描开始时的根发布序号；
- 扫描时槽保护的对象标识。

`claim()` 在发出许可前逐片原子复核注册版本和所有槽版本。发生变化时只把该分片
标记为失效，并把分片号放入 `ClaimResult::invalidated_shards`。调用方只需重扫
`invalidated_shards`。`stale_shards` 表示证据早于该批次的移除发布序号，
`missing_shards` 表示尚未扫描。三种集合可由 `needs_rescan()` 统一判断。

一批对象只有在以下条件全部成立时才得到许可：

- 每个存活线程的保护槽都在该批次移除发布之后被观察；
- 扫描期间注册或被复用编号的线程所在分片已重新覆盖；
- 没有任何槽保护该批次中的任一 `(address, generation)`；
- 所有相关分片在领取前仍保持扫描时的注册版本与槽版本。

批次状态用原子 CAS 从 pending 切到 issued。两个回收者即使都完成扫描和核验，
也只有一个能得到许可；另一个会在 `already_issued_batches` 中看到该批次。
`Permit::consume()` 再提供持有者侧的一次性消费检查。

## 线程生命周期

线程退出顺序固定为：

1. 清空自己的保护槽；
2. 释放线程编号；
3. 结束该分片的注册版本变更。

注册复用编号时也会先清空槽，再发布新 incarnation。扫描证据绑定 incarnation，
而不是只绑定编号，因此旧证据不能覆盖新线程。注册/退出是低频控制路径，
由一个互斥量串行化；读者的发布、二读和回收许可的 CAS 路径不使用该锁。

## 参考实现与测试

`reference_rescan_all()` 不保存分片证据，而是在每个批次上重新观察全部存活线程槽，
给出当前可回收与延迟回收的批次/对象集合。测试用它与分片实现逐次比较。

`tests/test_permission.cpp` 覆盖：

- 同地址、不同代次，以及旧代次保护不阻塞新代次；
- 读者已看见旧根但尚未发布保护时，许可仍可发出，随后二读失败；
- 扫描后注册新线程只使对应分片失效；
- 未扫描分片中的新线程被后续扫描覆盖；
- 线程退出并复用编号后不继承旧扫描证据，旧 guard 不清新槽；
- 32 轮两个真实线程竞争同一批次，只产生一个许可；
- 三类线程事件队列的 1260 条保序交错，每个 claim 都与全槽重扫参考实现比较
  允许/延迟对象集合。

## 边界

- `Authority` 是单索引的合成状态机；真实节点释放由宿主在消费许可后完成。
- 同一线程令牌不得同时持有两个重叠的 `ReadGuard`；需要嵌套保护时，应为嵌套
  读取分配独立线程令牌/槽。
- 批次许可是全有或全无；若批次中任一对象仍被保护，整批延迟。
- 宿主不能把同一 `(address, generation)` 重新挂回根。标识的代次必须单调且不回绕。
- `snapshot_for_reference()` 面向确定性的小规模交错检查，不作为生产并发快照 API。
