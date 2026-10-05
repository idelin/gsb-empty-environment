# request-dedup

请求去重库：记录只保留有限期限（`retentionMs`），通过**显式请求代次协议**保证
“记录过期后客户端重试”不会被误当作新请求重复执行。

## 协议

每个逻辑请求由三元组标识：

```ts
interface RequestIdentity {
  clientId: string;   // 客户端标识，代次在客户端维度内单调递增
  generation: number; // 代次：非负整数，客户端收到 indeterminate 后必须升代
  requestId: string;  // 代次内唯一的请求标识，同一逻辑请求的重试保持不变
}
```

`execute(identity, fn)` 的结果：

| kind            | 含义                                                         |
| --------------- | ------------------------------------------------------------ |
| `fresh`         | 本次调用真正执行了 `fn`                                       |
| `joined`        | 结果尚未产生，合并等待到进行中的执行上（`fn` 不会再跑一次）      |
| `replayed`      | 记录在保留期内，直接返回缓存结果（失败同样被缓存并原样抛出）     |
| `indeterminate` | 记录可能已被清理，**无法判定**是否执行过；`fn` 不会被执行        |

`indeterminate` 的两种成因（`reason` 字段）：

- `expired-record`：记录还在但已超过保留期（`now >= settledAt + retentionMs`）。
- `retired-generation`：记录已被清理，且该代次（或更老代次）已有记录被驱逐，
  无法区分“从未执行”与“执行后遗忘”。

客户端收到 `indeterminate` 后必须升 `generation` 并更换 `requestId` 再重试。

## 边界语义

- 记录在 `now < settledAt + retentionMs` 时有效；恰好在 `settledAt + retentionMs`
  时刻即视为过期。
- 过期即驱逐：访问时惰性驱逐，或通过 `cleanup()` 主动清扫；驱逐会把该客户端的
  代次水位线推进到被驱逐记录的代次。
- 完成与清理同刻：记录在完成时刻 `T` 的过期时间为 `T + retentionMs > T`，
  因此 `T` 时刻的清扫绝不会误删刚完成的结果。
- 进行中的记录不参与过期，不会被 `cleanup()` 驱逐。

## 使用

```ts
import { RequestDeduplicator } from './src/index.ts';

const dedup = new RequestDeduplicator({ retentionMs: 60_000 });

const result = await dedup.execute(
  { clientId: 'client-1', generation: 0, requestId: 'pay-123' },
  () => chargeCard(...),
);

if (result.kind === 'indeterminate') {
  // 升代后以新 requestId 重试
}

setInterval(() => dedup.cleanup(), 60_000).unref();
```

## 测试

需要 Node.js >= 22.6（原生 TypeScript 类型擦除）：

```sh
npm test
```

如需完整类型检查，安装 `typescript` 与 `@types/node` 后运行 `tsc --noEmit`
（`tsconfig.json` 已配置）。
