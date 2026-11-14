# netsec — RTSP 会话参数安全绑定组件

供 C++20 流媒体服务导入的会话参数组件，防御“复制他人会话 ID 的 SETUP 参数，把
媒体流重定向到攻击者地址”的会话劫持。

## 威胁模型与保证

- 会话在 `setup()` 时一次性绑定四要素：客户端来源地址、认证主体、传输参数、代次。
- 会话 ID 由操作系统 CSPRNG 生成 256 位随机数并做 base64url 编码，不可预测；每个
  ID 全局只签发一次，TEARDOWN 后进入永久墓碑，无法被重放或重新绑定。
- `play()` 在进入播放状态前逐项核对：来源地址（按 NAT 策略）、认证主体、整个
  `TransportParams`（目标 IP、端口、TTL、interleaved 通道等），任一不符即拒绝。
- `mediaTarget()` 只对“存在、处于 Playing、来源仍合法”的会话返回目的地址，且目的
  地址始终取自 SETUP 时的绑定，攻击者无法在 PLAY 阶段替换媒体去向。
- 每次接受的状态迁移推进单调递增的代次（generation），用于识别过期/重复请求。
- 全部操作在读写锁保护下进行，支持多线程并发 SETUP/PLAY。

## NAT 策略（显式选择，默认最严格）

`NatPolicy` 决定来源地址变化如何处理，不存在“隐式信任新地址”：

- `RejectChange`（默认）：IP 与端口都必须与绑定时完全一致。
- `SameIpAllowPortChange`：IP 不变，允许端口变化（典型 UDP NAT 重绑定）。
- `SameNetworkAllowRebind`：IPv4 同 /24、IPv6 同 /64 内允许重绑定到任意端口。
- `AuthenticatedRebind`：仅当请求携带与绑定相同的已认证主体时，才允许任意地址迁移；
  匿名或主体不符一律拒绝。

## 集成方式

组件是普通 C++ 静态库（`netsec.hpp` + `netsec.cpp`），可直接加入现有 CMake 工程：

```cmake
add_subdirectory(netsec)
target_link_libraries(your_rtsp_server PRIVATE netsec)
```

典型调用：

```cpp
#include "netsec.hpp"
netsec::SessionManager sessions(netsec::NatPolicy::SameIpAllowPortChange);

// SETUP：签发一次性会话 ID 并绑定来源与传输参数
netsec::RequestSource src{peerIp, peerPort, principal, authenticated};
std::string sessionId;
auto setup = sessions.setup(src, transportHeader, sessionId);

// PLAY：逐项复核来源、主体与传输参数，通过后才进入 Playing
auto play = sessions.play(sessionId, src, transportHeader);

// 发送每个媒体包前解析目的地址；非法/非播放会话返回 nullopt
if (auto target = sessions.mediaTarget(sessionId, src)) {
    sendRtp(target->ip, target->port, packet);
}
```

## 构建与测试

```sh
cmake -S . -B build -DNETSEC_SANITIZE=ON
cmake --build build
ctest --test-dir build --output-on-failure
```

`tests/session_test.cpp` 覆盖：会话 ID 复制与一次性、地址变化的策略判定、传输参数
替换、合法会话全流程与主体绑定、并发播放（16 线程 × 2000 次）下媒体只发往原客户端。
