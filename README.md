# async-resource-transaction

异步资源事务作用域（TypeScript，零依赖）。

## 语义

- **按序获取**：`scope.use(spec)` 按调用顺序获取资源并入栈。
- **逆序释放**：任一步失败（获取失败、业务异常、外部取消）即回滚，按逆序释放已取得资源。
- **错误保留**：触发回滚的原始错误保留在 `TransactionError.cause` / `acquireError`；
  回滚与接管过程中的全部释放错误收集在 `TransactionError.releaseErrors`
  （含获取序号 `index` 与 `name`），释放错误不会掩盖原始错误。
- **取消竞态 / 责任空隙**：取消可能发生在「获取已成功、但回调尚未处理」的窗口。
  此时作用域已关闭，迟到落地的资源所有权仍归作用域，由作用域**就地接管释放**；
  回滚会等待全部在途获取落地（drain 屏障）后才完成——
  因此 `runTransaction` 结算时，任何已完成的获取要么已提交移交、要么已被释放，
  不存在无人负责的空隙。

## 用法

```ts
import { runTransaction, TransactionError } from './src/transaction.ts';

const controller = new AbortController();

try {
  const result = await runTransaction(async (scope) => {
    const db = await scope.use({
      name: 'db',
      acquire: ({ signal }) => openConnection({ signal }),
      release: (conn) => conn.close(),
    });
    const lock = await scope.use({
      name: 'lock',
      acquire: () => acquireLock(db),
      release: (l) => l.unlock(),
    });
    return await doWork(db, lock);
  }, {
    signal: controller.signal,
    onCommitted: (resources) => { /* 所有权移交，之后由调用方 release */ },
  });
} catch (err) {
  if (err instanceof TransactionError) {
    console.error('起因:', err.cause);
    console.error('释放错误:', err.releaseErrors);
  }
}
```

也可以手动管理生命周期：`new TransactionScope()` + `use` / `commit` / `rollback`。

## 关键设计

- `use()` 在作用域关闭后**同步快速失败**（`ScopeClosedError`）。
- 在途获取被计数跟踪；`rollback()`/`commit()` 先推进已入栈资源的释放/移交，
  再等待在途获取落地：迟到资源由作用域接管释放（失败计入 `releaseErrors`），
  其 `use()` 调用以 `ScopeClosedError` 拒绝。
- `commit()` 成功后将资源句柄（`{ index, name, value, release }`）移交给调用方；
  已提交作用域不可再回滚。

## 运行测试

需要 Node ≥ 22.6（依赖原生 TypeScript 类型擦除运行）：

```sh
npm test
# 或
node --test "test/*.test.ts"
```
