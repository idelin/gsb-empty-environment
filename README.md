Go宿主让插件通过句柄访问文件和网络资源，需要可导入的能力路由包。句柄绑定插件实例、资源类型、允许操作和不可回绕代次，能力只能由宿主铸造，插件可传任意字节。宿主代理调用必须带当前调用链，每次嵌套代理权限取整条链的交集，不能借高权限代理扩大权限；委托只能收窄，循环调用链拒绝，最大深度32。支持实例卸载与重载，关闭资源后已有句柄拒绝；取得操作租约先于关闭的调用可结束，关闭返回后不能新进入资源回调。测试跨类型重解释、委托增权、代理洗权限、旧代次与并发关闭，记录拒绝原因但不暴露其他租户资源身份。
## 实现：`caproute` 包

可导入的能力路由包位于 `caproute/`，仅依赖标准库。

- 句柄：`Host.MintCapability` 是唯一铸造入口，生成 32 字节随机不透明 `Handle`，绑定实例、`ResourceType`、允许 `Operation` 集合与铸造时的 `Generation`；插件传入任意字节时，未命中记录一律返回同一 `invalid_handle` 错误。
- 调用链：`Host.Entry` 建立首帧，`Host.Proxy` 每跳追加一帧；有效权限是整条链的交集（`CallChain.Effective`），高权限代理无法为低权限调用方扩权。
- 委托：`Host.Delegate` 只接受源句柄已包含的操作，任何增权请求返回 `delegation_widening`，结果句柄仍绑定同一资源并加盖当前代次。
- 链约束：重复实例判定为循环（`call_chain_cycle`），帧数达到 `MaxChainDepth = 32` 后拒绝新跳（`call_chain_depth_exceeded`）。
- 生命周期：`Reload` 单调推进代次并使旧句柄返回 `stale_generation`；`UnloadInstance` 吊销句柄并关闭其独占资源；`CloseResource` 先阻止新进入，再等待所有已取得租约的回调排空后返回。
- 审计：每次拒绝写入 `AuditSink`（默认 `MemoryAudit`），含结构化 `Reason` 与宿主侧资源标签；返回给插件的 `DenialError` 只含原因码，不含其他租户的资源身份。

### 运行测试

```sh
go test ./caproute/ -race -count=1
```
