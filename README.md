# 断线续传模拟器（Resume Simulator）

模拟断线客户端持游标重连的恢复协议，覆盖服务端已压缩旧记录的情形。
纯 TypeScript、零依赖，直接由 Node（>= 22.6，类型擦除）运行。

## 运行

```sh
npm run simulate   # 端到端场景模拟（含断言）
npm test           # node:test 测试套件
```

## 协议

### 游标

游标是不透明 token（base64url JSON）：`{ v, filterHash, seq }`。

- `seq`：已消费到的全局单调序列号；
- `filterHash`：订阅条件规范化（排序、去重）后的 SHA-256 截断哈希，
  **游标与订阅条件绑定，不可跨条件复用**；
- `v`：协议版本，不支持的版本直接拒绝。

校验结果（`src/cursor.ts`）：

| 情形 | 结果 |
| --- | --- |
| 无法解码 / 结构非法 | `CURSOR_MALFORMED` |
| 版本不支持 | `CURSOR_UNSUPPORTED_VERSION` |
| 订阅条件不匹配 | `CURSOR_FILTER_MISMATCH` |
| `seq < compactedBefore`（已被压缩） | `CURSOR_TOO_OLD` → 走快照恢复 |
| 有效 | `resumed` + 精确重放 |

### 消息流

```
subscribe(filter, cursor?) →
  snapshot { entries, atSeq, cursor }   # 无游标或游标过旧
  resumed  { fromSeq, cursor }          # 游标有效
  event    { event, prevSeq, cursor }*  # 重放 + 实时
  live                                  # 已追平
```

### 快照与增量切换（无遗漏）

1. 记下 `atSeq = log.latestSeq()`，开始生成快照（耗时可模拟，
   期间事件继续写入日志）；
2. 发出 `snapshot`（截至 `atSeq` 的状态 + 以 `atSeq` 为起点的游标）；
3. **先注册实时监听（事件入队缓冲），再从日志重放 `seq > atSeq`
   的事件**，最后排空缓冲转入实时流；两段覆盖区间有重叠，按 seq
   去重——日志是唯一事实来源，交界处不丢、不重、不乱。

### 客户端校验

- 每条 `event` 携带 `prevSeq`，与本订阅流上一条投递形成链
  （被过滤的事件不在链上，因此过滤订阅也能精确校验）；
  链基线由 `snapshot.atSeq` / `resumed.fromSeq` 建立，断裂即缺口；
- `event.seq <= lastSeq` 的事件为**可明确识别的重复**
  （重连时持久化游标落后于已应用位置所致），不重复应用。

## 文件

- `src/event-log.ts` — 仅追加日志 + 压缩（旧事件折叠进基准状态）
- `src/cursor.ts` — 游标编解码与校验
- `src/server.ts` — 恢复协议服务端（快照/重放/实时切换）
- `src/client.ts` — 客户端（链校验、重复识别、检查点）
- `src/simulator.ts` — 六阶段端到端场景
- `test/resume.test.ts` — 协议测试
