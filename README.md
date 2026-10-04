# request-dedup

带有限保留窗口的请求去重库，采用**显式请求代次协议**（explicit request
generation protocol），保证过期记录被清理后，迟到的重试绝不会被静默当作
新请求再次执行。

## 协议

```ts
import { RequestDeduplicator } from 'request-dedup';

const dedup = new RequestDeduplicator({
  pendingTtlMs: 1_000, // 在途记录保留期限
  resultTtlMs: 5_000,  // 结果保留窗口
});

// 1. 客户端先开启请求，获得显式代次（需自行持久化以便重试时携带）
const generation = dedup.begin(key);

// 2. 携带代次执行；同一 key + 代次只会执行一次
const outcome = await dedup.execute(key, generation, () => doWork());
```

`execute` 的返回是判别联合 `Outcome<T>`：

- `{ status: 'ok', value }` — 首次执行或命中窗口内缓存的结果；
- `{ status: 'error', error }` — `fn` 失败，失败同样作为终态在窗口内缓存；
- `{ status: 'undetermined' }` — 记录已过期或代次未知，**无法判定**原请求
  是否执行过；库保证此时绝不调用 `fn`。客户端若接受风险，必须显式再次
  `begin` 获取新代次后才能执行。

## 语义要点

- **合并等待**：结果尚未产生时，携带同代次的并发 `execute` 共享同一个
  在途 Promise，`fn` 只运行一次，所有等待者拿到同一结果。
- **幂等开启**：记录存活期间重复 `begin` 返回同一代次；记录过期后
  `begin` 签发单调递增的新代次，旧代次永久失效（返回 `undetermined`）。
- **同刻规则**：记录完成的那一刻会把过期时间刷新为
  `完成时刻 + resultTtlMs`，因此与首次完成**同刻**发生的窗口清理
  （`sweep`）不会清除刚产生的结果——完成在同刻优先于清理。
- **惰性过期**：除 `sweep` 主动清理外，所有访问路径（`begin` / `execute`
  / `has`）都会先按当前时钟淘汰过期记录，行为与是否调用 `sweep` 无关。
- **迟到完成**：若记录在 `fn` 完成前已被清理，已挂接的等待者仍能收到
  结果，但结果不再落库，后续重试得到 `undetermined`。

时钟通过 `clock` 选项注入（默认 `Date.now`），测试可使用
`ManualClock` 精确控制同刻场景。

## 运行测试

```sh
npm test   # node --test（Node >= 22.6，原生类型擦除，无需编译）
```
