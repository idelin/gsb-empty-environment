# netsec：CoAP 块传输安全组件

供 C++20 CoAP 服务端导入的块状态组件。它解决的核心问题是：不能用 token
作为块缓存的唯一键，否则复用 token 请求另一资源时，后续块会与前一块拼接。

## 构建

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

CMake 目标为 `netsec`，公共头文件为 `netsec.hpp`。默认构建共享库，可用
`-DBUILD_SHARED_LIBS=OFF` 构建静态库。

## 接收块（Block1/响应块组装）

```cpp
#include "netsec.hpp"

netsec::BlockTransfer transfer;

netsec::Block block;
block.key.token = netsec::bytes_from_hex("01020304");
block.key.method = "PUT";
block.key.uri = "/upload/42";
block.key.content_format = 50;
block.key.block_size = 64;
block.key.start_sequence = 0;
block.key.requester = {"tls-session-7", "192.0.2.10:5683"};
block.key.total_length = 130;                 // 与 ETag 绑定的表示总长度
block.key.etag = netsec::bytes_from_string("v1");

block.sequence = 0;
block.etag = netsec::bytes_from_string("v1");
block.more = true;
block.payload = /* 恰好 64 字节 */;

netsec::BlockResult result = transfer.receive(block);
if (result.status == netsec::BlockStatus::Complete) {
    // result.body 是按序拼接并已校验长度的完整内容
}
```

状态查找键是请求者会话（`session_id` + `endpoint`）和 token。状态内部还保存
完整绑定：token、方法、URI、内容格式、块大小、起始序号、请求者身份。任意绑定
字段变化都会拒绝并删除旧状态；设置
`BlockTransferOptions::restart_on_context_mismatch = true` 后，若新块正好是
起始块，则从新绑定重新开始。

块校验规则：

- 首块必须等于 `start_sequence`，后续序号必须逐一连续；跳跃返回
  `SequenceGap`，不追加数据。
- 所有块的 ETag 必须一致；不一致返回 `EtagMismatch` 并使状态失效。
- `more == true` 的块必须恰好为 `block_size` 字节，最后一块不得超过
  `block_size`。
- 总长度必须等于 `BlockKey::total_length`；也可用
  `transfer.register_etag(etag, length)` 按 ETag 登记长度，完成时强制校验。
- 已接收过的块返回 `Duplicate`；内容冲突的重复块返回
  `ConflictingDuplicate`；过期或未接收过的旧序号返回 `Stale`。三者都不会改变
  已组装内容。
- 传输超过 TTL 后被清除；旧序号块返回 `Expired`，起始块可以重新开始。

## 发送块（Block2 下载）

`BlockServer` 保存服务端表示并按请求返回块。响应状态同样绑定完整 `BlockKey`：

```cpp
netsec::BlockServer server;
netsec::Representation representation{
    netsec::bytes_from_string("resource body"),
    netsec::bytes_from_string("v1"),
    50,
};

netsec::BlockRequest request;
request.key = block.key;          // 同一完整绑定
request.sequence = 0;
request.etag = netsec::bytes_from_string("v1"); // 可选条件 ETag

netsec::BlockReply reply = server.serve(request, representation);
```

沿用同一 token 但改变 URI 时，默认返回 `ContextMismatch` 并删除旧响应状态；
开启 restart 选项后，起始块会用新表示重新开始。任何情况下都不会取到旧 URI
表示的后续字节。

## 观察关系

观察关系存放在独立的 `ObservationKey` 表中：

- `observe()` / `cancel_observation()` 不会创建或删除块状态。
- `reset()` / `sweep_expired()` 不会删除观察关系。
- 观察键包含请求者身份，不会跨会话共享。

`BlockTransfer` 的所有公开操作都由互斥量保护，不同会话、不同 token 的并发
请求可安全并行；同一会话的不同 URI 会被完整绑定比较拦截。

## 测试覆盖

`tests/block_transfer_test.cpp` 覆盖：

- token 复用同一会话请求另一 URI（拒绝和重启两种策略）；
- 方法、URI、内容格式、块大小、起始序号变化；
- 块序号跳跃、重复块、过期块；
- ETag 不一致、总长度与 ETag 元数据不一致；
- 合法连续续传、跨会话隔离、观察关系隔离；
- 多线程并发请求，以及 Block2 下载只返回同一请求的块。
