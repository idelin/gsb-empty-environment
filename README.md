# 控制流返回栈校验

插桩运行时要区分正常异常展开与被篡改的返回地址。实现供Go测试宿主调用的控制流组件，事件包含函数进入、间接调用、返回、建立恢复点和异常展开；影子栈记录调用点及模块代次，展开只能到同线程仍存活的恢复点，间接目标必须属于调用点允许的类型集合。模块卸载后旧目标失效。用递归、恢复点重用及跨线程伪造事件验证最早拒绝位置。

## 组件：`cflow`

`cflow` 包是由插桩测试宿主以合成事件驱动的控制流完整性核心，入口见
`cflow/cflow.go`。

### 数据模型

- `Runtime`：全局状态，内部加锁，所有方法可并发调用；按线程保存独立状态。
- `FuncInfo` / `CallSite`：函数与调用点静态元数据。间接调用点带
  `AllowedTypes` 类型集合，直接调用点只校验目标代次。
- 影子栈帧 `frame`：记录 `funcID`、创建该帧的 `callSite`、进入时捕获的模块
  代次 `gen`，因此被篡改的返回地址无法与栈顶或旧镜像匹配。
- 恢复点 `recoveryPoint`：绑定线程与建立时的栈深；所属帧返回或被展开弹出后
  标记为死亡，ID 可重新使用。

### 事件语义

| 事件 | 校验与状态变化 |
| --- | --- |
| `EventFuncEntry` | 目标函数已注册且模块在役；非根帧的调用点必须存在于调用者；压入影子栈 |
| `EventIndirectCall` | 调用点必须是间接点；目标模块在役且目标类型属于允许集合，否则在该事件处拒绝 |
| `EventReturn` | 空栈报 `ReasonReturnEmpty`；函数与栈顶不符报 `ReasonReturnMismatch`；返回地址所属代次已退役报 `ReasonModuleUnloaded`；通过后弹栈并清理失效恢复点 |
| `EventRecoveryPoint` | 同线程存活的同 ID 重复建立报 `ReasonRecoveryExists`；在当前栈顶帧上记录着陆点 |
| `EventUnwind` | 仅能命中同线程仍存活的恢复点，跨线程伪造在查找阶段即报 `ReasonRecoveryNotFound`；不弹任何帧报 `ReasonUnwindTop` |

`Process` 在第一个违规事件处停止并返回 `*Violation`（含零基 `Index`、事件
副本、`Reason*` 原因与诊断信息），违规事件不产生任何状态变更，因此
`Violation.Index` 即最早拒绝位置。也可用 `errors.Is` 匹配 `ErrTypeMismatch`
等哨兵错误。

### 模块代次

`RegisterModule` 返回递增代次，`UnloadModule` 将当前代次标记为退役（负值）。
卸载后：

- 任何进入或间接调用解析到旧镜像立即以 `ReasonModuleUnloaded` 拒绝；
- 旧影子栈帧保留其 `gen` 供诊断，经旧帧返回同样拒绝；
- 重新装载获得新代次，同函数 ID 重新生效但与旧帧代次不同。

### 测试宿主

`go test ./... -race` 运行 `cflow` 包测试，覆盖：

- 递归压栈与伪造返回（`TestRecursionAndTamperedReturn`）；
- 间接调用类型集合违规与直接/间接点误用（`TestIndirectCallTypeEnforcement`）；
- 模块卸载、旧目标失效与重载代次（`TestModuleUnloadInvalidatesOldTargets`）；
- 恢复点同帧重用、ID 回收与生命周期（`TestRecoveryPointReuseAndLifecycle`）；
- 跨线程伪造展开的最早拒绝位置（`TestCrossThreadForgedUnwind`）；
- 并发宿主下的线程隔离（`concurrent_test.go`）。
