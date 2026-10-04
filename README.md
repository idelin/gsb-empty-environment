# 连接重置模拟器（新代次握手）

模拟“发送端崩溃重启、序号归零”的经典问题：旧连接滞留网络的残包与新连接
的合法数据在序号空间上完全重叠，接收方若无额外防护会把旧数据当新数据交付。
本实现用**双方确认的新代次握手**解决该问题，并用确定性虚拟时间网络验证
确认丢失、双方同时重启、旧重置消息迟到三类异常场景。

## 运行

需要 Node ≥ 22.6（推荐 23，直接运行 TypeScript）：

```sh
nvm use          # 或任意 Node >= 22.6
npm test         # 11 个协议测试
npm run sim      # 场景演示与结论输出
```

## 协议设计

### 持久状态（重启不丢）

| 字段 | 含义 |
| --- | --- |
| `gen` | 本地代次号，单调递增，每次重启 +1 并立即落盘 |
| `maxPeerGenSeen` | 已知对端最大代次号，用于识别迟到的旧 RESET |

关键不变式：对端旧 incarnation 发出的任何报文，其 `gen` 必然小于对端当前
代次。因此 ESTABLISHED 后只需比对 `DATA.gen === peerGen` 即可拒收全部旧残包。

### 报文

| 报文 | 字段 | 含义 |
| --- | --- | --- |
| `RESET` | `gen, nonce` | 我方进入新代次，发起握手 |
| `RESET_ACK` | `gen, nonce, ackGen, ackNonce` | 我方代次 + 确认你方代次 |
| `CONFIRM` | `gen, nonce, ackGen, ackNonce` | 确认你方代次，握手完成 |
| `DATA` | `gen, seq, payload` | 数据，`seq` 每代次从 0 重计 |

### 状态机

```
IDLE --connect()/restart()--> RESET_SENT --RESET_ACK(匹配)--> ESTABLISHED
RESET_SENT --RESET(对端新代次)--> ACK_SENT        （同时重启：交叉握手）
ACK_SENT --RESET_ACK(匹配)--> ESTABLISHED         （同时重启的收敛路径）
ACK_SENT --CONFIRM(匹配)--> ESTABLISHED
ACK_SENT --DATA(gen=peerGen, seq=0)--> ESTABLISHED（隐式确认，CONFIRM 丢失时）
ESTABLISHED --RESET(对端新代次)--> ACK_SENT       （对端再次重启：拆除重建）
任意 --RESET(gen < maxPeerGenSeen)--> 忽略        （旧重置迟到）
```

握手报文按超时重传（RESET / RESET_ACK），重复报文幂等重答；
`nonce` 保证只接受属于本次握手的应答。

### 握手期间的数据缓冲

- 接收侧：握手未完成（或乱序）的数据进入**有界缓冲**（`recvBufferCap`，
  默认 4），溢出即丢弃并计数，绝不提前交付、绝不无界堆积；
  握手完成时按新代次过滤缓冲，再按序号顺序释放。
- 发送侧：握手未完成时应用数据排队（`sendQueueCap`，默认 4），溢出丢弃；
  建连后盖上新代次与从 0 开始的序号补发。

## 何时可以安全交付第一条数据

接收方交付第一条数据（`seq=0`）的充要条件，三者同时满足：

1. **本端状态机已 ESTABLISHED** —— 三次握手完成，双方互相确认过对方代次：
   发起方收到与本地 nonce 匹配的 `RESET_ACK`；响应方收到 `CONFIRM`，
   或收到对端新代次的 `seq=0` 数据（隐式确认）。
2. **`DATA.gen === peerGen`** —— 代次号持久且单调递增，旧连接残包的
   `gen` 必然小于当前 `peerGen`，必然失配被拒。
3. **`seq === expectedSeq`（首包为 0）** —— 序号空间随代次重新计数。

## 已验证场景（test/reset.test.ts）

- 基本握手与首数据安全交付（含交付不变式断言）
- 发送端重启序号归零：旧残包拒收；朴素模式（不校验代次）对照实验演示误收
- 确认丢失：RESET_ACK 丢失（重传恢复）、CONFIRM 丢失（隐式确认 /
  RESET_ACK 重传两条路径）
- 双方同时重启：交叉握手收敛到同一代次对
- 旧重置消息迟到：旧 RESET / RESET_ACK 被识别忽略，新连接不受影响
- 缓冲上限：握手期接收缓冲溢出丢弃、恢复后按序交付；发送队列上限
- 对端再次重启：旧代次缓冲在拆除时清空

## 文件

- `src/messages.ts` — 报文定义
- `src/endpoint.ts` — 端点状态机（协议核心）
- `src/network.ts` — 确定性虚拟时间网络（时延/丢包/扣留/释放）
- `src/simulate.ts` — 场景演示脚本
- `test/reset.test.ts` — 协议测试
