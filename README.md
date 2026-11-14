# 发布报文可信状态校验

供 C++20 变电站通信栈直接包含/链接的发布报文校验组件。它解决“重放旧报文并递增 `stNum` 后，订阅者误用伪造开关状态”的问题。

## 校验规则

一个 `PublishValidator` 对应一个订阅。报文必须依次满足：

- `appId`、目的 MAC、`goId`、`dataSet` 与订阅配置完全一致。
- `configVersion` 与订阅配置一致；配置版本或订阅身份变更后重新建立序列。
- 报文时间戳不早于“接收时间 - `maxTimestampAge`”，不晚于“接收时间 + `maxFutureSkew`”。
- `timeToLive` 为正数、不超过配置硬上限，且报文年龄不超过该生存时间。
- `stNum` 不得回退；新状态必须递增，并携带比上一可信状态更新的时间戳。这样即使重放报文仍在 TTL 内，篡改 `stNum` 也会因旧状态时间戳被拒绝。
- 同一 `stNum` 内 `sqNum` 必须递增；相同或更小的 `sqNum` 分别按重放/序列回退丢弃。
- 任何检查失败都不推进可信状态高水位。

未知数据集、APPID/MAC/goID 替换、配置版本不符、重放、回退、时间过期、TTL 超限均返回明确的 `RejectReason`。

## 集成示例

```cpp
#include "netsec.hpp"

netsec::SubscriptionConfig config;
config.appId = 0x1234;
config.destinationMac = {0x01, 0x0c, 0xcd, 0x01, 0x00, 0x01};
config.goId = "IED1/LLN0$GO$gcb01";
config.dataSet = "IED1/LLN0$DS$ds01";
config.configVersion = 7;

netsec::PublishValidator validator(config);

netsec::PublishMessage message;  // 由报文解析器填充
message.appId = config.appId;
message.destinationMac = config.destinationMac;
message.goId = config.goId;
message.dataSet = config.dataSet;
message.stNum = 10;
message.sqNum = 0;
message.timestamp = receivedAt - std::chrono::milliseconds{20};
message.timeToLive = std::chrono::seconds{2};
message.configVersion = config.configVersion;

const auto result = validator.validate(message, receivedAt);
if (result.accepted) {
    // 仅在此处应用报文中的开关状态。
} else {
    // result.reason: replay / state_rollback / sequence_rollback / ...
}
```

`validate(message)` 使用系统时钟；`validate(message, receivedAt)` 接受显式接收时间，适合网关测试和确定性回放。`trusted_state()` 返回最近一次完全通过校验的 `stNum`、`sqNum`、时间戳和配置版本。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

测试覆盖合法发布与重传、状态号伪造、APPID/MAC/goID/数据集替换、重放、`stNum`/`sqNum` 回退、时间与 TTL 边界、配置版本切换以及相同配置重载不丢失重放高水位。

本组件校验发布报文信封的可信性，不替代报文签名/MAC 等密码学认证；能够修改时间戳并重新构造报文的攻击者仍需由上层认证机制阻断。
