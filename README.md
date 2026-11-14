# HTTP/1.1 连接响应配对器

`netsec` 是一个 C++20 静态库，用于代理后端 HTTP/1.1 连接上的严格请求/响应配对。
一个 `netsec::ConnectionPairer` 实例只代表一条后端连接。组件不会猜测、跳过或重排
响应：只要响应不能与请求队列头部唯一确认，就毒化并关闭该连接。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖合法流水线、响应错序、提前关闭、长度/分帧不符、超时、深度限制、
后端错误上限和连接复用。

## 核心保证

- **每连接严格 FIFO**：请求只能从队首出队；未完整校验的响应不会返回给调用方。
- **方法与状态校验**：HEAD/204/304 不允许消息体；1xx 只能作为中间响应；
  101（协议切换）和 CONNECT 2xx 隧道直接拒绝，因为后续字节无法再按 HTTP 配对。
- **长度校验**：`Content-Length` 必须与消息体完全相等；chunked 必须严格解析到
  终止块和 trailer；`Transfer-Encoding` 与 `Content-Length` 同时出现即视为
  分帧岐义。
- **歧义即关闭**：重复且冲突的 `Content-Length`、裸 LF、obs-fold、非法 chunk、
  缺长度且不能由 EOF 确认的响应、以及无法确认归属的响应都会关闭连接。
- **有界状态**：流水线深度、头部大小、消息体大小、中间响应数、分帧开销、
  请求/响应超时和后端错误次数均可配置。
- **复用前复位**：`prepare_reuse()` 只有在队列为空、无半截响应、无残留字节时
  才成功；否则关闭连接。成功后 `generation()` 递增，旧连接的迟到字节不会进入
  新使用者的状态。

## 使用方式

结构化响应入口（调用方已完成 HTTP 分帧，长度字段必须与实际 body 一致）：

```cpp
#include "netsec.hpp"

netsec::ConnectionPairer conn;

netsec::Request req;
req.id = 42;
req.method = "GET";
req.target = "/account";
if (!conn.enqueue(req).ok()) {
  // Do not send req on the backend connection.
}

netsec::Response resp;
resp.status = 200;
resp.body = "secret";
resp.content_length = resp.body.size();  // or a Content-Length header

auto result = conn.on_response(42, std::move(resp));
if (!result.ok()) {
  // Close the backend socket and discard all queued requests.
} else {
  for (auto& delivery : result.deliveries) {
    // delivery.request.id identifies the only client allowed to receive this page.
  }
}
```

结构化入口收到的对象已经具有确定的 body 边界；若没有提供 `Content-Length`/
`chunked`，组件会以该完整对象的 body 长度作为权威长度。原始字节入口没有这个
信息，因此对 keep-alive 上无长度分帧的响应一律按歧义关闭。

原始后端字节入口（适合直接转发 socket 数据）：

```cpp
auto result = conn.feed(bytes_from_backend);
if (!result.ok()) {
  close_backend_socket();
} else {
  for (auto& delivery : result.deliveries) {
    send_to_client(delivery.request.id, delivery.response);
  }
}
```

连接放回池前必须调用 `prepare_reuse()`；失败时只能销毁连接。

## 错序检测

HTTP/1.1 响应本身不携带请求 ID，穷举所有错序在协议层是不可能的。组件提供三层
防护：

1. 默认按 HTTP/1.1 规定的位置 FIFO 配对，任何方法/状态/分帧不自洽都关闭连接。
2. 结构化入口可为每个响应附带 `request_id`，或使用
   `on_response(request_id, response)`；与队首不一致立即报
   `out_of_order_response`。
3. 原始字节入口可配置 `Config::response_id_header`（如 `X-Request-Id`）。代理向
   后端请求注入唯一值，后端回显后，组件在交付前核对请求的
   `response_token`（或十进制请求 ID）。缺失或不一致即关闭连接。

对于两个方法、状态和长度都完全相同的响应，只有第 2/3 层能识别交换；位置 FIFO
本身无法区分。高安全场景应启用绑定头。

## 配置默认值

| 配置 | 默认值 |
| --- | --- |
| `max_pipeline_depth` | 32 |
| `max_interim_responses` | 8 |
| `max_header_bytes` | 64 KiB |
| `max_body_bytes` | 8 MiB |
| `max_frame_overhead_bytes` | 256 KiB |
| `max_backend_errors` | 1 |
| `request_timeout` / `response_timeout` | 30 s |
| `allow_close_delimited` | 仅在唯一未完成请求、显式 close 或 EOF 可确认时接受 |
| `require_explicit_reuse` | false；启用后每次复用必须调用 `prepare_reuse()` |

所有入口都接受可选的 `TimePoint` 参数，便于事件循环和测试注入时间。失败结果通过
`Result::failure` 返回 `ErrorCode`、是否致命及说明；致命错误已经清空队列和解析器，
调用方只需关闭 socket。
