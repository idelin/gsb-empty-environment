# LSM 钩子 inode 决策与审计

eBPF LSM 钩子拒绝打开受保护文件，攻击者借硬链接、重命名或绑定挂载换路径，且审计记录与实际决定不一致会掩盖拒绝。为C++17守护组件实现供模拟内核钩子调用的决策与审计包：策略按（设备号，inode）保护文件，路径提示只写入日志；多个程序按挂载顺序执行，任一返回拒绝即最终拒绝，后续程序不再执行；每次钩子调用恰产生一条审计，内容含最终决定、第一个拒绝的程序和 inode；环形缓冲写满时仍按策略拒绝并累加丢失计数，下一条成功记录携带丢失数。用硬链接别名、重命名后访问、多程序拒绝顺序和缓冲写满验证。

## 核心语义

- `netsec::FileId{device, inode}` 是策略唯一键；`path` 仅进入 `AuditRecord`，不参与判定。
- `HookEngine::on_open` 按 `add_program` 的注册顺序执行程序；第一个 `Deny` 立即短路，后续程序不执行。
- 未注册程序拒绝时，内置策略仍会按 `(device, inode)` 判定；策略拒绝记为 `policy` 程序。
- 每次钩子调用只调用一次 `RingBuffer::push`，并产生一个 `AuditRecord`。
- 环形缓冲满时保留原决定，只累计丢失数；下一条成功写入的审计记录在 `dropped`、`lost`、`dropped_events` 中携带该数。

## 接口示例

```cpp
#include <netsec.hpp>

netsec::Policy policy;
policy.protect(device, inode, "/real/path");

netsec::AuditRing audit(1024);
netsec::HookEngine guard(policy, audit);
guard.add_program("first", [](const netsec::HookContext&) {
    return netsec::Decision::Allow;
});
guard.add_program("second", [](const netsec::FileId&, const std::string&) {
    return netsec::Decision::Deny;
});

auto result = guard.on_open(device, inode, "/alias/path");
// result.decision == Deny, result.denying_program == "second"
// result.record.inode == inode, result.record.path == "/alias/path"
```

常用入口：

- `Policy::protect(FileId, path_hint)`、`Policy::protect(device, inode, path_hint)`
- `HookEngine::on_open`、`open`、`handle`、`decide`
- `RingBuffer::push`、`pop`、`records`、`dropped`、`total_dropped`
- `HookEngine::audit_ring()`、`policy()`、`programs()`

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖硬链接别名、重命名后访问、多程序拒绝短路与执行顺序、审计唯一性、异常拒绝以及环形缓冲满后的丢失数结转。
