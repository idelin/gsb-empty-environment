# async-resource-transaction

异步资源事务作用域库（TypeScript，零依赖）。

## 语义

- **顺序获取**：资源通过 `scope.acquire(acquire, release)` 依次获取。
- **逆序释放**：任一步失败（获取失败 / 业务回调抛错 / 外部取消）时，已取得的资源按 LIFO 逆序释放。
- **取消安全（无责任空隙）**：取消可能发生在“获取已成功、回调尚未处理”的窗口期。
  此时资源已到手但尚未登记，由作用域**接管并立即释放**——每个成功获取的资源
  必然恰好被释放一次，不会泄漏，也不会重复释放。
- **错误保留**：首个失败原因（获取错误 / 业务错误 / 取消原因）与回滚期间的
  全部释放错误都被保留。无释放错误时原样抛出首个失败原因；否则抛出
  `TransactionError`，其 `cause` 为首个失败原因，`releaseErrors` 按发生顺序
  保留全部释放错误。
- **提交语义**：业务回调成功返回即提交，资源所有权移交调用方（不释放）；
  作用域随即封闭，之后再调用 `acquire`/`defer` 会立即抛错。

## 用法

```ts
import { transaction, TransactionError } from './src/transaction';

const controller = new AbortController();

try {
  const [db, cache] = await transaction(async (scope) => {
    const db = await scope.acquire(openDatabase, (db) => db.close());
    const cache = await scope.acquire(connectCache, (c) => c.quit());
    scope.defer(() => metrics.flush());
    return [db, cache]; // 提交：所有权移交调用方
  }, { signal: controller.signal });
  // ...使用 db / cache，调用方负责最终释放
} catch (error) {
  if (error instanceof TransactionError) {
    console.error('首个失败原因:', error.cause);
    console.error('释放错误:', error.releaseErrors);
  }
  throw error;
}
```

## 关键实现点

- `acquire()` 中 `await` 返回后、登记前**没有任何 await**：状态检查与登记
  在同一个同步区间内完成，取消事件无法插入其间形成空隙。
- 若 `await` 期间作用域已开始回滚，迟到的资源由 `acquire()` 就地接管释放，
  其释放错误同样汇入 `releaseErrors`。
- 回滚先等待在途获取完成所有权交接（登记或接管释放），再统一逆序释放，
  保证严格的 LIFO 顺序。
- 回滚 Promise 永不 reject：所有释放错误都被收集而非抛出，确保回滚一定走完。

## 构建与测试

```sh
npm run build   # tsc -p tsconfig.json
npm test        # 编译并运行 test/transaction.test.ts
```

测试覆盖：顺序获取、失败逆序释放、获取/释放错误双保留、预取消、
窗口期取消的迟到资源接管、接管释放失败的错误聚合、关闭后拒绝获取、
defer 逆序清理，以及 300 轮随机取消时机的压力测试
（断言无泄漏、无重复释放、严格逆序）。
