# C++17 证书撤回与票据重算组件

`certrev` 是一个可嵌入准入宿主的小型证书撤回组件，没有第三方依赖。核心实现位于
`include/certrev/revocation.hpp`，完全展开的穷举参考器位于
`include/certrev/reference.hpp`。

## 建模要点

- `Receipt` 绑定签署者、设备代次 `generation`、底层 `physical_range`、版本化逻辑映射
  `LogicalMapping` 和完整证明根 `proof_root`。
- `AdmissionTicket` 列出若干逻辑或物理测量要求；票据可固定一个映射版本。
- 同一可能设备状态默认由 `(generation, proof_root)` 标识。不同证明根是不同分叉，
  不能为每个测量分别挑选不同分叉后拼成一张票据。
- 逻辑别名先通过已认证映射换算成物理区间，再按物理槽求并集。两个逻辑别名映射到同一
  底层槽时，不会被当成两个独立物理槽。
- 撤回范围包括：
  - 签署者，可限定设备代次，并支持从某个新代次恢复；
  - 底层物理子区间，可限定代次；
  - 单个逻辑映射版本；
  - 额外提供收据 ID 和完整证明根撤回。

## 三态结论

- `TicketStatus::Kept`：存在一份完整、当前可用且状态一致的证明组合。
- `TicketStatus::Pending`：当前没有可用组合，但存在一个若未知签名/证明验证为真即可
  成立的完整组合。
- `TicketStatus::Revoked`：在已展开的已知证据中不存在任何可能成立的组合；所有候选均
  已确定失效、冲突或无法跨分叉组合。

`RevocationComponent::reevaluate()` 只输出相对上次基线发生状态变化或保留支持指纹变化
的票据。`newly_withdrawn` 是此前保留、现在变为 `Revoked` 或 `Pending` 的最小票据集合；
`definitively_revoked`、`newly_pending` 和 `restored_to_kept` 分别给出收敛方向。

## 独立复验

`Decision::support` 是自包含的 `SupportBundle`，复制了最小 witness 使用的全部收据、
票据、解析后的物理测量、物理槽并集、映射版本和撤回版本号。宿主可把它交给
`verifySupportBundle()`，并通过 `BundleVerifiers` 注入真实的签名和 Merkle/证明验证。
省略验证回调时使用宿主已填入的 `VerificationState`；未显式标记为 `Verified`
的签名、证明或映射按 `Pending` 处理。无论是否提供回调，组件都会复核结构、状态组合、
映射、物理槽一致性和撤回状态。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_CXX_STANDARD=17
cmake --build build -j
ctest --test-dir build --output-on-failure
```

测试覆盖：

- 共享收据/共享证明根撤回；
- 部分物理子区间失效，旁侧槽位保留；
- 签署者恢复但新信任只从其他设备代次开始；
- 未知签名/证明经新信息收敛为 `Kept` 或 `Revoked`；
- 多个逻辑别名映射同一物理槽时只形成一个物理并集；
- 禁止逐项选择不同 `proof_root` 分叉；
- 只撤回一个映射版本时，物理票据和其他映射不受影响；
- 500 个确定性随机小银行与 `reference::fullyExpandedEvaluate()` 全展开结果差分对照，
  并复验所有 `Kept` 支持包。

示例程序见 `examples/admission_host.cpp`。

## 嵌入方式

直接把 `include/` 加入头文件路径，或在 CMake 中使用：

```cmake
add_subdirectory(path/to/certrev)
target_link_libraries(admission_host PRIVATE certrev)
```

典型流程：

```cpp
certrev::RevocationComponent component;
component.addReceipt(verified_receipt);
component.addTicket(issued_ticket);
component.prime();

component.revocations().revokeRange({4, 5}, /*generation=*/7);
certrev::TicketDiff diff = component.reevaluate();

for (const certrev::Id& id : diff.newly_withdrawn) {
    block_admission(id);
}

for (const certrev::SupportBundle& support : diff.remaining_support) {
    auto check = certrev::verifySupportBundle(
        support, component.revocations(), verifiers);
    if (!check.valid) block_admission(support.ticket_id);
}
```

组件本身不实现具体密码学算法；`VerificationState` 和 `BundleVerifiers` 是宿主密码学
验证结果进入撤回判定的信任边界。
