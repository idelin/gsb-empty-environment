# C++20 HPKE 上下文管理组件

`netsec::hpke` 为消息宿主提供 HPKE 上下文生命周期、RFC 9180 密钥调度和
exporter 派生。组件不内置密码算法：KEM、KDF、AEAD 参数通过 `SuiteCallbacks`
注入，敏感结果统一使用 `OpaqueHandle`。

## 主要约束

- `setup_base`、`setup_psk`、`setup_auth`、`setup_auth_psk` 对应四种 HPKE 模式；
  PSK 模式必须同时提供非空 PSK 和 `psk_id`，Auth 模式必须提供发送方静态钥句柄及
  公钥身份。
- 不可变 `ContextMetadata` 绑定完整 `SuiteId`、原始 `enc`、接收方/发送方公钥身份、
  `info`、模式、PSK identity 和握手代次。
- 密钥调度缓存按 `KEM || KDF || AEAD` 完整套件、模式、代次、`enc`、双方公钥、
  `info`、`psk_id` 和 PSK 指纹分区。PSK 原值不进入缓存键。
- exporter 只接受 `make_label(...)` 生成的命名空间内 label；派生使用
  `LabeledExpand(exporter_secret, label, raw_context, L)`，并限制 label、context
  和输出长度。
- 所有临时 KEM/KDF 句柄由 RAII 释放。Setup 失败、低阶点 Decap、PSK 校验失败和
  代次轮换都会触发销毁；`rotate_receiver_key()` 会使旧上下文立即失效。
- 每个 suite 独立持有回调与销毁函数，因此两个 suite 回调即使返回相同数值的
  opaque 句柄，也不会混用缓存或销毁路径。

## 使用方式

```cpp
#include "netsec.hpp"

netsec::hpke::HpkeContextManager manager("message-host");
manager.register_suite(suite_id, suite_callbacks);

netsec::hpke::SenderSetupParams params;
params.suite = suite_id;
params.receiver_public_key = recipient_pk;
params.info = transcript_info;

auto context = manager.setup_base(params);
auto stream_key = context.export_secret(
    manager.make_label("stream"),
    exporter_context,
    32);
```

发送方与接收方需要各自实现 `encapsulate`/`decapsulate`。`KdfExtractInput` 带
`secret` 句柄时，KDF 回调必须对 `prefix || secret_bytes(handle)` 执行 Extract；
否则对 `bytes` 执行。

## 测试

`tests/netsec_tests.cpp` 使用独立 opaque 句柄空间的确定性 mock KEM/KDF，覆盖：

- 四种模式及双方 exporter 一致性
- 两个 suite 返回相同 opaque 编号时的缓存隔离
- base/auth 混淆、PSK 值/`psk_id` 差异、`info` 差异、接收方公钥差异
- 空 PSK、不完整 PSK、低阶点 Decap、Setup 失败清理
- labeledExpand 线格式、命名空间/长度边界
- 并发 Export 和 Setup 期间异步密钥轮换

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```
