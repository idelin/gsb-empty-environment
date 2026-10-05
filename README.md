# 连接重置模拟器（Connection Reset Simulator）

模拟一个经典难题：**发送端崩溃重启后序号归零，旧连接在网络中残留的报包（残包）可能被
新连接误当作新数据交付**。本实现用"双方确认的新代次（generation）三次握手"解决该问题，
并对握手期间的应用数据提供**有上限的缓冲**。

运行要求 Node.js ≥ 22.6（原生 TypeScript 类型擦除，无需编译、无第三方依赖）：

```sh
node --test test/*.test.ts   # 运行全部测试
node src/demo.ts             # 运行带报文轨迹的演示
```

## 协议设计

### 代次（Generation）

每个节点每次启动（`boot()`）产生一个新代次：

```
Generation = { node, incarnation, nonce }
```

- `incarnation`：持久化的单调递增计数器，每次重启 +1（模拟写盘存储）；
- `nonce`：每次启动的随机数，防止同一 incarnation 的重放。

每条报文携带代次对 `(srcGen, dstGen)`。一条连接由代次对 `(G_a, G_b)` 唯一标识；
任何一代次不匹配的报文都是残包，一律拒收（必要时回 RESET）。

### 三次握手（双方确认新代次）

```
发起方 I                          响应方 R
   |  SYN      {srcGen:Gi}         |
   | ----------------------------> |
   |  SYN-ACK  {srcGen:Gr, dstGen:Gi, ack: isnI+1}
   | <---------------------------- |
   |  ACK/DATA {srcGen:Gi, dstGen:Gr, ack: isnR+1}
   | ----------------------------> |
```

- I 确认 R 的代次：只有收到 `dstGen == Gi` 且 `ack == isnI+1` 的 SYN-ACK 才进入
  ESTABLISHED——该报文只可能在本代次握手开始之后被构造；
- R 确认 I 的代次：只有收到 `dstGen == Gr` 且 `ack == isnR+1` 的第三条报文
  （ACK 或携带数据的 DATA）才进入 ESTABLISHED；
- 双方同时发起（同时重启）时走同时打开：SYN 交叉后各自转入 SYN-RECEIVED，
  互答 SYN-ACK 后同时 ESTABLISHED，只产生一条连接；
- SYN / SYN-ACK 超时重传（有次数上限），最终 ACK 丢失由响应方重传 SYN-ACK 补救。

### RESET 的回显校验

收到无法匹配当前连接的报文时，接收方回 RESET，并**回显**触发报文的
`(srcGen, dstGen, seq)`。本端仅在以下三个条件同时满足时才接受 RESET：

1. `echoSrcGen == 本端当前代次`（它确实在回应我们这一代发出的报文）；
2. `echoDstGen == 当前对端代次`（它针对的是当前这条连接，而不是上一条）；
3. `echoSeq ∈ [sndIsn, sndNxt)`（序号落在本代次已发送窗口内）。

因此旧 RESET 迟到时，其回显的代次对与新连接不同，被安全忽略；
而合法 RESET 被接受后，本端拆除旧状态并以新代次对重新握手。

### 序号

每条连接（每次握手）选取新的随机 ISN，SYN 占一个序号，数据从 `ISN+1` 开始。
即使残包序号与接收窗口完全重叠，代次校验也会先于序号校验将其拒收
（见测试「旧连接残包不会被当作新数据交付」）。

## 何时可以安全交付第一条数据

**不变式：`deliver()` 只在 ESTABLISHED 状态、报文代次对等于已建立代次对、
且 `seq == rcvNxt` 时发生。**

具体到两侧：

- **发起方**：收到满足 `dstGen == Gi ∧ ack == isnI+1` 的 SYN-ACK 之时起。
  该报文证明响应方已经看到并接受了本代次 Gi——它不可能是一个旧连接的残包，
  因为 Gi 在本次启动前从未存在过。
- **响应方**：收到满足 `dstGen == Gr ∧ ack == isnR+1` 的第三条握手报文
  （ACK 或 DATA）之时起。该报文证明发起方收到了本代次的 SYN-ACK，
  握手双方就此互相确认：此后凡代次对不匹配的报文都可确定是残包。

换言之，**每一侧都必须先收到一条"只有在本代次握手开始后才可能被构造出来"的
报文**，才允许向应用交付数据；握手完成前到达的数据一律进入有界缓冲或拒收。

## 握手期间的有界缓冲

`send()` 在连接建立前把应用数据放入缓冲队列，总字节数超过
`maxBufferedBytes` 时返回 `false`（背压，由应用决定重试或放弃）。
进入 ESTABLISHED 的瞬间按 FIFO 顺序冲刷缓冲；已发出未确认的数据由超时
重传保证恰好一次交付。

## 测试覆盖

| 场景 | 文件 |
| --- | --- |
| 正常三次握手、首数据序号 = ISN+1、先建连后交付 | `test/handshake.test.ts` |
| 旧代次残包（序号完全重叠）被拒收并回 RESET | `test/handshake.test.ts` |
| 握手 ACK 丢失 → SYN-ACK 重传、无重复交付 | `test/handshake.test.ts` |
| 数据包丢失 → 超时重传、恰好一次 | `test/handshake.test.ts` |
| 对端重启 → RESET 回显校验 → 自动重握手 | `test/reset.test.ts` |
| 旧 RESET 迟到 → 忽略，新连接不受影响 | `test/reset.test.ts` |
| 双方同时重启 → 交叉 SYN 建立唯一连接、双向残包被拒 | `test/reset.test.ts` |
| 迟到的旧 SYN 残包 → 无法建立幽灵连接 | `test/reset.test.ts` |
| 握手期间缓冲上限与按序冲刷（双向） | `test/buffering.test.ts` |

## 假设与限制

- `incarnation` 必须持久化且单调递增；若无法持久化，重启后需静默
  ≥ 2·MSL 才能保证旧代次报文全部消亡（本模拟器用持久化计数器规避）；
- 防的是"网络故障 + 节点重启"，不防主动攻击者（真实系统需认证/加密）；
- 单连接模型：一个 Endpoint 同一时刻只维护一条连接；
- 接收侧不按序缓存，乱序数据直接丢弃并依赖发送方重传（go-back-N）。
