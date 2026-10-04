# request-coalescer

请求合并库：相同键的在途加载只执行一次，每个调用者保留自己的截止时间与取消通道。

## 语义保证

- **合并**：同一键的在途请求共享一次 `loader` 调用，结果广播给所有调用者。
- **独立截止时间**：`load(key, { timeoutMs })` 只影响该调用者；超时以 `CoalescerTimeoutError` 拒绝，不影响同批其他人。
- **独立取消**：`load(key, { signal })` 中止只移除该调用者（`CoalescerAbortError`），不取消批次。
- **最后一人退出即中止**：批次调用者归零时，从注册表摘除该批次并 `abort()` 加载信号，loader 可据此释放资源。
- **迟到结果不覆盖新批次**：批次以代（generation）为单位隔离；被中止/已了结批次的迟到 resolve/reject 直接丢弃，不会波及同键的新批次。
- **同步异常**：`loader` 同步抛错时，当前批次同步失败并清理，调用者收到该错误，后续调用可正常重试。
- **重入安全**：`abort()` 触发前先把批次从注册表摘除，因此中止回调里可以重入调用 `load()`（同键也会开启新的一代）。

## API

```ts
const coalescer = new RequestCoalescer<string, Value>({
  loader: async (key, signal) => fetchValue(key, signal),
  schedule, // 可选，默认 setTimeout/clearTimeout；测试注入手动调度器
});
const value = await coalescer.load('key', { timeoutMs: 1000, signal: ctrl.signal });
```

## 测试

`npm test` 用手动调度器（`ManualScheduler`）确定性地推进时间，覆盖：合并、独立截止时间、单独取消、末位退出中止、迟到结果/迟到拒绝隔离、loader 同步异常、中止回调重入、预中止信号、多键独立。

本环境无网络无法安装 TypeScript 编译器，因此 `npm test` 通过 `scripts/strip-types.mjs` 把"可擦除语法"子集的 TS 源擦除为 JS 后在 Node 上直接运行（源码约束见该文件头注释）。联网环境下可 `npm install` 后用 `npm run typecheck` 做完整类型检查。
