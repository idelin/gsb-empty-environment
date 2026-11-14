# 分支预测器安全域隔离组件

进程切换后，分支预测器可能保留上一进程的训练状态。攻击者可以先训练某个
分支，再让受害者执行同一地址，从预测结果推断受害者的执行路径（Spectre-v2
一类攻击）。本仓库提供一个可被 C++20 调度组件导入的软件模型与调度钩子。

组件把预测器状态按 **CPU × 安全域** 分区，每个表项都带完整标签：

```
domain id | domain label | epoch | cpu | sensitive
```

预测或训练时只有完整标签与当前驻留域一致才会命中。标签不匹配的旧表项、
其他域的表项会被视为缺失，而不是被当作预测结果。敏感域拥有独立的
`domain label`；未显式提供标签时，组件使用进程级随机种子为每个域派生
唯一标签。

## 调度组件接口

```cpp
#include "netsec.hpp"

netsec::IsolatorConfig cfg;
cfg.seed = 0x1234;                              // 0 表示使用随机种子
cfg.wipe_sensitive_on_cross_domain = true;     // 高保障部署可选
netsec::PredictorIsolator iso(cfg);

iso.register_domain({.id = 1, .sensitive = false, .name = "attacker"});
iso.register_domain({.id = 2, .sensitive = true,  .name = "victim"});

iso.context_switch(1, cpu);          // 调度器切换
auto p = iso.predict(1, pc);         // 执行分支前查询
iso.train(1, pc, actual_taken);      // 分支解析后训练

iso.enter_interrupt(irq_domain, cpu); // 中断/异常进入
iso.return_from_interrupt(cpu);       // 中断返回

iso.migrate(1, old_cpu, new_cpu);     // 任务迁移
```

也可以调用 `switch_to`、`on_context_switch`、`on_interrupt`、
`on_interrupt_return`、`on_migration` 等别名。所有接口内部串行化，可从
不同 CPU 的调度路径调用。

## 隔离语义

- **上下文切换**：同一安全域内切换是合法的空操作，保留已训练状态；
  跨域切换不会把 A 域表项暴露给 B 域。设置
  `wipe_sensitive_on_cross_domain` 后，敏感域离开 CPU 时其分区会被清空并
  递增 `epoch`。
- **中断**：被中断的域压入每 CPU 的中断栈，handler 使用自己的分区；支持
  嵌套中断。敏感域按同一策略保留独立分区或擦除。
- **迁移**：分区从源 CPU 移出并清除，目标 CPU 获得同一域的表项；非敏感域
  的表项会重新打标签并保留计数。敏感域可配置为迁移时擦除。源 CPU 后续
  调度的其他域只能看到自己的分区。
- **跨域训练检测**：显式对非驻留域调用 `train`/`predict` 会被拒绝并产生
  `CrossDomainTraining` / `CrossDomainPrediction` 告警；非零标签不匹配也会
  被拒绝。
- **异常预测率检测**：每个域维护滑动窗口；样本数达到
  `anomaly_min_samples` 且误预测率超过
  `anomaly_mispredict_threshold` 时产生 `PredictionRateAnomaly` 告警。
- **状态导入**：`export_snapshot` / `import_snapshot` 绑定域标签和 epoch，
  跨域重放会以 `StateImportRejected` 拒绝；跨 CPU 导入必须显式允许迁移。

## 验证

测试覆盖跨进程训练、路径推断、合法切换、嵌套中断、迁移、敏感域擦除策略、
异常预测率检测和状态导入：

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build
ctest --test-dir build --output-on-failure
```

## 部署注意

- 该组件是调度器侧的软件模型与隔离策略实现，不能替代硬件侧的
  IBRS/retpoline、微码更新或进程隔离。
- 安全域边界由调度器定义。不可信进程与敏感进程必须映射到不同域；只有
  明确互信的进程才应共享同一个非敏感域。
- 调度器必须在 CPU 上下文切换、中断进入/返回、CPU 热插拔和任务迁移的
  所有路径调用对应钩子；遗漏钩子会破坏隔离假设。
- 对高保障敏感域建议启用 `wipe_sensitive_on_cross_domain`，并把
  `history_window`、`entries_per_domain` 设置得足够小，以降低跨域容量
  争用和残留。
- `stats()`、`export_snapshot()` 和告警日志仅供内核/调度器侧可信代码读取，
  不应暴露给非特权用户态。
