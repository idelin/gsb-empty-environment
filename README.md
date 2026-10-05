# gc — 显式批量环回收库（C++17）

节点之间用**强引用**（`std::shared_ptr`）连边构成图，因此环靠引用计数永远
无法释放。本库提供一个显式的批量收集器：在不把边改成弱引用的前提下，
回收"不再被根集合触达"的子图（包括任意形状的环）。

## 核心概念

- **强引用边**：`Node::link(child)` 持有 `shared_ptr`，环可以自然存在。
- **外部根集合**：`Collector::add_root / remove_root`，两次收集之间可随意变化。
  未被登记的外部句柄不算根（这是契约：想保住对象就必须登记为根）。
- **显式批量收集**：`Collector::collect()` 同步执行一个 epoch，返回退休节点数。
- **析构日志**：每个节点析构时把名字追加到共享日志 `Collector::log()`，
  用于核验"被销毁集合"与"存活集合"（`Collector::live_set()`）恰好划分全部节点。

## 阶段边界

```
Idle ──collect()──▶ Mark ──────────────▶ Sweep ──────────────▶ Idle
              快照根集合            退休集合原子生效          延迟根生效
              计算可达集            切断垃圾子图的边          清理跟踪表
              （可登记新根，        （退休节点拒绝读写，
                下一 epoch 生效）     析构写入日志）
```

- **Mark**：对根集合做快照，DFS 计算可达集。`collect(hook)` 的 hook 在
  Mark 阶段执行，允许登记新根——但这些根**延迟到下一 epoch 才生效**，
  不能救活本轮已被判定不可达的节点。
- **Sweep**：先把整个不可达集合**原子地**标记为 retired（此后任何
  `read()/write()/link()` 抛出 `retired_access_error`），再统一切断边。
  因此不会有人观察到"切了一半"的图。
- **退休 ≠ 立即析构**：若外部仍持有未登记的强引用，节点处于 retired 状态
  但尚未析构；读取它会抛异常，直到最后一个强引用消失才写入析构日志。
  给退休节点登记根同样抛异常——不允许复活。

## 构建与测试

```sh
cmake -B build && cmake --build build
ctest --test-dir build --output-on-failure
```

## 示例

```cpp
gc::Collector gc;
auto a = gc.make_node("a");
auto b = gc.make_node("b");
a->link(b);
b->link(a);          // 强引用环
gc.add_root(a);

gc.collect();        // a、b 可达，全部存活
gc.remove_root(a);
gc.collect();        // 环不再可达：a、b 被回收，名字进入析构日志
```

## 假设与限制

- 单线程使用；阶段边界由 `collect()` 的同步执行定义。
- 节点只能经 `Collector::make_node` 创建，收集器通过弱引用跟踪全量节点。
- 不可达性的判定完全基于登记的根集合；未登记的外部强引用只能推迟析构，
  不能阻止退休。
