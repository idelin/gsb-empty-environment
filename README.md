# 航空可信报文来源组件

供 C++20 航空报文网关导入的 `netsec::MessageSource`。组件在转发前完成严格的来源、格式、时效、序列、签名和关键指令确认校验；任何拒绝都不会消耗来源序列，因此补齐确认或修正报文后可以重新提交。

## 构建

```sh
cmake -S . -B build
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

## 接入方式

```cpp
#include "netsec.hpp"

netsec::SourcePolicy policy;
policy.local_address = "AOC";

netsec::MessageSource source{policy};
source.add_source("GROUND1", ground_key);   // 登记表

const auto result = source.accept(message);
if (!result) {
    // result.error / result.detail 可用于审计；报文不进入运行控制。
}
// result.message 只包含已经通过全部检查的可信报文。
```

`InboundMessage` 是传输层解析后的结构化报文。类型、源地址和目的地址会规范为大写；报文负载保持原样。

## 校验顺序

- 类型必须在 `SourcePolicy::allowed_types` 中，来源必须已登记，且可选来源角色限制必须允许该类型。
- 目的地址必须符合格式；配置 `local_address` 时必须精确匹配本运行控制地址。
- 时间只接受 `YYYY-MM-DDTHH:MM:SSZ`（可带最多 9 位小数秒），并受 `max_age` 和 `max_future_skew` 限制。
- 序列号必须非零且严格大于该来源上一次已接受的值；接受记录的摘要保留在 `replay_window` 中，重复内容直接拒绝。
- 负载只允许可打印 ASCII；默认拒绝换行、控制字符和非 ASCII 字符。类型/地址使用受限标识符字符集，所有字段都受长度限制。
- 非关键类型中出现 `CLIMB`、`CLEARED`、`DIRECT`、`MAINTAIN` 等操作指令时按注入拒绝。
- 所有报文默认要求 HMAC-SHA-256 地面签名。关键指令还必须包含二次确认引用和独立的确认签名。

## 签名

签名输入是带长度前缀的规范化报文，避免字段拼接歧义：

```cpp
message.signature = netsec::sign_message(message, ground_key);
```

关键指令的确认签名先对报文核心内容取摘要，再用同一地面密钥签名确认上下文：

```cpp
message.confirmation = "READBACK-001";
message.confirmation_signature =
    netsec::make_confirmation_signature(message, ground_key);
message.signature = netsec::sign_message(message, ground_key);
```

也可以把二次确认放在独立结构中提交：

```cpp
netsec::ConfirmationProof proof{
    .reference = "READBACK-001",
    .signature = confirmation_signature,
};
const auto result = source.accept(message, proof);
```

## 安全边界

- 组件不保存明文私钥文件；密钥由调用方通过登记表提供。生产环境应从受保护的密钥存储加载，并为每个来源使用独立密钥。
- HMAC 只证明持有共享密钥的一方签发了报文。若需要跨组织不可抵赖性，应把同一接口后的签名实现替换为经审计的 Ed25519/ECDSA 地面签名。
- 组件负责报文准入，不替代网关的传输层身份认证、审计日志、速率限制和下游转义。拒绝路径应记录来源、错误码和报文摘要，不应记录密钥。
