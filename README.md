# CAN FD帧身份与新鲜度过滤

攻击者用29位扩展ID冒充同数值的11位ID，或回放带旧计数器的帧，穿过车载网关进入动力总线。给C++固件写供转发路径调用的CAN FD帧过滤组件：白名单键含ID与IDE位，DLC按CAN FD表映射长度（9到15对应12至64字节）且载荷长度须一致；每个ID有最小周期；带新鲜值的ID要求计数严格递增，截断MAC由调用方函数核验。用IDE混淆、超速帧和回放验证。

## 转发路径接入

`netsec::CanFdFilter` 使用固定容量表，不分配内存。转发路径只需提供帧、单调递增的微秒时间戳和新鲜计数器：

```cpp
netsec::CanFdFilter filter(verifyTruncatedMac, &macContext);
filter.addEntry({
    0x123, netsec::CanFdIde::Standard, 8,
    10'000,   // 最小周期：10 ms
    true,     // 计数器必须严格递增
    true,     // 需要调用方校验截断 MAC
});

if (filter.accept(canFrame)) {
    forwardToPowertrainBus(canFrame);
}
```

`filter.accept(frame)` 依次检查：

- 白名单以 `(id, ide)` 为完整键，标准 ID 和扩展 ID 不会被合并。
- 入帧 DLC 必须与配置一致；DLC 0..8 对应 0..8 字节，9..15 对应 12、16、20、24、32、48、64 字节；`payloadLength` 必须等于 DLC 映射值。
- 同一 ID 的相邻已接受帧间隔不得小于 `minPeriodUs`，时间戳回退同样拒绝。
- `requiresFreshCounter` 的条目要求计数器严格大于上次已接受值。
- `requiresMac` 的条目调用调用方提供的 `CanFdMacVerifier`；也可以使用 `accept(frame, macVerified)` 传入已完成的校验结果。

只有全部检查通过后，过滤器才写入时间戳和计数器状态。被拒绝的帧（包括 MAC 失败的帧）不会推进状态。网络/ECU 重启导致计数器重新开始时调用 `resetTracking()`，白名单保持不变。

`canFdPayloadLength()` 暴露 CAN FD DLC 映射，`isValidCanFdDlc()` 用于区分非法 DLC 与合法的零长度载荷。

## 测试

`tests/netsec_test.cpp` 覆盖 DLC 表、载荷长度一致性、同数值 11/29 位 ID 混淆、超速帧、时间戳回退、旧计数回放及 MAC 失败不推进状态。
