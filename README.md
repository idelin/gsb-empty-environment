# 固定对象移动与复用检测

异步运行时让第三方 Future 插件保存自引用指针，插件若在被固定后被移动或其内存被复用，自引用指针就会悬垂。为C++17模拟运行时写供调度器调用的固定不变量检测库：对象一旦登记为固定，其槽位地址不得再被 memcpy、swap 或赋值覆盖，也不得在析构完成前释放或复用；内部自引用指针须始终等于登记槽位内的地址。未登记为可移动类型的对象被移动即报告违规及首次违规事件。用固定后交换、析构前复用槽位、可移动类型与合法固定生命周期验证。

## 用法

调度器持有 `netsec::PinRegistry`，在对象生命周期关键点调用：

- `pin(slot, size, &obj.self, name, movable)`：登记固定对象；`self` 为对象内部自引用指针字段的地址。
- `notify_write(addr, n)`：memcpy / swap / 赋值写入时调用，与固定槽位重叠即报 `SlotOverwritten`。
- `notify_move(from, to)`：未登记为可移动的对象被移动时报 `IllegalMove`；可移动对象迁移登记并以新槽位为基线。
- `notify_reuse` / `notify_free` / `unpin`：析构完成（`notify_destruct_end`）前释放、复用或注销槽位即报 `PrematureReuse`。
- `check` / `check_all`：在 poll/await 边界校验槽位字节（FNV-1a 校验和）与自引用指针（`*self_ptr == slot`，否则 `DanglingSelfPointer`）。
- `violations()` / `first_violation()`：违规事件序列（含单调序号）与首次违规。

## 构建与测试

```sh
cmake -S . -B build && cmake --build build
ctest --test-dir build --output-on-failure
```

测试位于 `tests/pin_invariant_tests.cpp`，覆盖：合法固定生命周期、固定后交换、析构前复用槽位、可移动类型迁移与非法移动、赋值覆盖与自引用悬垂。
