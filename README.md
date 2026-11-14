# 车载总线信号授权组件

面向 C++20 车载网关的可导入库。网关不得把原始总线帧直接转发给 ADAS；每个帧先经过标识、发送者、周期、消息认证、信号范围和冲突来源校验，只有授权信号才会发布给下游。拒绝帧不会覆盖最后一次可信值，并会进入降级状态。

## 校验顺序

1. CAN 标识必须出现在策略中，载荷长度必须精确匹配。
2. 发送者必须与帧策略一致；同一信号冲突组内的其他生产者会被拒绝。
3. 关键帧必须携带有效的截断 HMAC-SHA-256 标签，标签覆盖 key id、CAN id、发送者、序号、时间戳、载荷长度和载荷。
4. 序号必须递增，时间戳不得回退，接收周期必须落在期望周期 ± 容差内。
5. 信号按 Intel/Motorola 位布局解码，应用 scale/offset 后必须在闭区间范围内。
6. 拒绝任何异常帧并置位降级状态；`trusted_signal()` 只返回此前已授权的值，并标记其是否新鲜。

诊断与刷写使用不同的会话策略和能力类别。会话请求的 HMAC 绑定会话类型、principal、nonce 和时间戳；诊断 grant 不能调用刷写服务，刷写 grant 也不能调用诊断服务。

## 集成

```cmake
add_subdirectory(path/to/netsec)
target_link_libraries(gateway PRIVATE netsec::netsec)
```

```cpp
#include "netsec.hpp"

netsec::AuthenticationKey speed_key = netsec::make_authentication_key(
    1, std::span<const std::uint8_t>(key_bytes));

netsec::AuthorizationConfig config;
config.keys.push_back(speed_key);

netsec::FrameSpec speed;
speed.identifier = 0x100;
speed.sender_id = 0x20;
speed.expected_period_us = 20000;
speed.period_tolerance_us = 2000;
speed.payload_size = 8;
speed.critical = true;
speed.authentication_key_id = 1;
speed.signals.push_back(netsec::SignalSpec{
    .name = "vehicle_speed",
    .conflict_group = "vehicle_speed",
    .start_bit = 0,
    .bit_length = 16,
    .scale = 0.01,
    .minimum = 0.0,
    .maximum = 250.0,
});
config.frames.push_back(speed);

netsec::SignalAuthorizer authorizer(std::move(config));

// timestamp_us 使用网关的单调接收时钟。
auto decision = authorizer.authorize(frame);
if (!decision) {
    auto fallback = authorizer.trusted_signal("vehicle_speed", now_us);
    // fallback->fresh == false 时，下游应使用安全降级值。
}
```

可信发送端用 `compute_frame_authentication_tag(key, frame)` 填标签；关键帧场景中不得把密钥或已签名帧缓存暴露给诊断/刷写面。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

`tests/netsec_tests.cpp` 覆盖合法车速/转向帧、帧注入、周期过短/过长、信号越界、冲突生产者、缺失/错误/篡改 MAC、重放、诊断与刷写权限隔离，并使用独立 HMAC 向量核对标签计算。
