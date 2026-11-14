# SMB 3.1.1 预认证协商状态组件

`netsec` 是一个无外部依赖的 C++20 库，供 SMB 客户端导入使用。它把每条 TCP
连接上的 SMB 3.1.1 预认证完整性哈希、协商结果解析、会话身份和证明校验放在
同一个强事务边界内，避免多通道或重连时把第二条连接的状态误当成原会话状态。

## 安全不变量

- **逐连接、从全零开始**：每个 `PreauthIntegrityChain` 初始值都是 64 字节全零；
  `H(n) = SHA-512(H(n-1) || raw_message_n)`。连接之间不能通过 `reset()` 共享进度；
  已开始哈希的链调用 `reset()` 会抛出 `ChainResetNotAllowed`。
- **禁止重编码**：`append()` 直接哈希调用者给出的完整 SMB2 原始消息，包括 SMB2
  头和所有上下文字节；组件只做边界/结构校验，不会重新序列化。
- **事务式解析**：`ChannelNegotiation::append()` 先完整校验消息，再推进哈希和阶段。
  截断、乱序、重复上下文、未知必选算法等错误不会留下部分哈希或部分会话。
- **不可变会话身份**：`SessionIdentity` 只在成功建立会话时由库内部构造，绑定
  dialect、cipher、signing 算法、preauth hash 算法、server GUID、证书 SHA-256、
  主机身份和原始会话 preauth hash。它没有公开构造函数或 setter。
- **多通道认证**：加入辅助通道前，使用原始会话中已认证的 session key 对**该通道
  自己的完整 transcript** 计算并校验 HMAC-SHA256 证明；同时要求所有安全参数、
  server GUID 和证书/主机身份与不可变身份完全一致。任何一项不同都不会发布通道。
- **重连只消费原身份**：重连 API 只接受原会话导出的 `SessionIdentity`。新 TCP 连接
  的 preauth 链仍从全零开始，但返回身份保留原会话的 preauth hash；更低 dialect、
  不同 GUID、不同 cipher/signing/peer 身份均被拒绝。重连 key 还通过身份中的 key
  commitment 绑定，不能换成攻击者自选 key。
- **异步失败/乱序完成**：`AsyncSessionNegotiation` 为每个连接维护独立 attempt；
  失败的 attempt 立即退役，乱序完成的 attempt 不会被发布；只有完整 transcript 和
  proof 都通过后才原子地加入会话。

## 主要 API

### 原始哈希链

```cpp
netsec::PreauthIntegrityChain chain;   // 64 字节全零
chain.append(netsec::MessageKind::NegotiateRequest, raw_request);
chain.append(netsec::MessageKind::NegotiateResponse, raw_response);
chain.append(netsec::MessageKind::SessionSetupRequest, raw_setup_request);
chain.append(netsec::MessageKind::SessionSetupResponse, raw_setup_response);

// 新 TCP 连接必须使用新对象，不能复用上一条连接：
netsec::PreauthIntegrityChain next =
    netsec::PreauthIntegrityChain::new_connection();
```

### 单连接协商

```cpp
netsec::ChannelOptions options;
options.channel_id = 0;
options.peer.host = "files.example.test";
options.peer.certificate_sha256 = peer_certificate_sha256;

netsec::ChannelNegotiation channel(options);
channel.append(netsec::MessageKind::NegotiateRequest, raw_negotiate_request);
channel.append(netsec::MessageKind::NegotiateResponse, raw_negotiate_response);
channel.append(netsec::MessageKind::SessionSetupRequest, raw_session_setup_request);
channel.append(netsec::MessageKind::SessionSetupResponse, raw_session_setup_response);

netsec::ChannelTranscript transcript = channel.finish();  // 失败不会返回部分状态
```

解析器会检查 SMB2 头、消息长度、SESSION_SETUP 状态、NEGOTIATE context 边界、
重复 context、重复算法、未知必选算法，以及响应选择的 dialect/cipher 是否在请求中
提供过。

### 建立、加入多通道和重连

```cpp
const netsec::Bytes key = authenticated_session_key_from_session_setup;
netsec::Bytes proof = netsec::compute_preauth_proof(key, transcript);

netsec::SessionNegotiation session =
    netsec::SessionNegotiation::establish(transcript, key, proof);
const netsec::SessionIdentity original = session.identity();

// 辅助通道：先得到它自己的 transcript 和 proof，再加入。
netsec::BoundChannel joined = session.join(aux_transcript, aux_proof);

// 重连：只能使用原会话导出的不可变身份。
netsec::SessionNegotiation reconnected =
    netsec::SessionNegotiation::reconnect(original, reconnect_transcript,
                                          key, reconnect_proof);
```

`compute_preauth_proof()` 的输入域包含 channel id、完整安全参数、peer 身份、
原始 transcript 最终 preauth hash 和消息数量。HMAC 只使用已认证 session key；
不使用 TLS/连接层临时密钥。

### 异步连接

```cpp
auto async = netsec::AsyncSessionNegotiation::establish(transcript, key, proof);
auto attempt = async.begin_channel(channel_options);
async.append(attempt, netsec::MessageKind::NegotiateRequest, raw_request);
async.append(attempt, netsec::MessageKind::NegotiateResponse, raw_response);
async.append(attempt, netsec::MessageKind::SessionSetupRequest, raw_setup_request);
async.append(attempt, netsec::MessageKind::SessionSetupResponse, raw_setup_response);

if (!async.complete(attempt, aux_proof, &error)) {
    // attempt 已退役，session.channels() 不会包含任何部分状态
}
```

也可以调用 `async.fail(attempt)` 显式中止。已失败或已完成的 handle 会返回
`StaleAttempt`。

## 错误处理

所有同步校验失败抛出 `netsec::Error`，其 `code()` 是 `netsec::ErrorCode`。常用错误：

- `MessageTruncated` / `MessageNotSmb2`：消息不完整或非 SMB2。
- `UnexpectedMessage`：NEGOTIATE/SESSION_SETUP 顺序错误。
- `DuplicateNegotiateContext` / `DuplicateAlgorithm`：重复 context 或算法。
- `UnknownPreauthAlgorithm` / `UnknownCipherAlgorithm`：未知必选算法。
- `DialectDowngrade` / `DialectMismatch` / `ServerGuidMismatch`：第二连接降级或上下文替换。
- `CipherMismatch` / `SigningMismatch` / `PeerIdentityMismatch`：未继承原安全协商。
- `ProofMismatch`：原始字节被改写、key 不对或通道 proof 不属于该 transcript。

无异常接口包括 `try_establish()`、`try_join()`、`try_reconnect()` 和
`AsyncSessionNegotiation::complete()`。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：

- 预认证哈希的零初始化、逐消息 SHA-512 计算和原始字节敏感性；
- 第二通道 dialect 降级、GUID 替换、cipher/signing/peer 不匹配；
- 新连接从零开始且重连不替换原会话 hash；
- 原始响应字节改写导致 hash/proof 失配；
- 重复 context、未知必选算法、消息截断、错误 SESSION_SETUP 状态；
- 消息乱序、异步 attempt 失败、完成先于消息；
- 合法多通道乱序完成，所有通道继承同一不可变身份和协商参数。
