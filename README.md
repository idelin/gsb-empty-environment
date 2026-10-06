# tiny-interpreter

小型 TypeScript 解释器：指令式 AST（`yield` / `do` / `try`/`finally` / `return`），
通过显式 `step()` 接口驱动，支持协作式取消。

## 语义

- **step 接口**：每次 `step()` 运行到下一个 `yield`（返回 `{type:"yield"}`）
  或程序结束（返回 `{type:"return"}`）；未捕获异常直接从 `step()` 抛出。
- **取消**：`cancel()` 只登记请求，在下一次从 `yield` 恢复的“恢复点”注入
  `CancelledError`（首次启动不算恢复点）。
- **try/finally**：`finally` 在正常、return、异常、取消每条退出路径上恰好
  执行一次；`finally` 内部可以继续 `yield`，解释器随之挂起，恢复后继续清理。
- **清理异常覆盖**：`finally` 自身抛出（含清理期间再次取消）时，清理异常
  覆盖原异常向外传播，原异常作为 `cause` 保留；清理值非 `Error` 时包装为
  `CleanupError`（原始值在 `cleanup` 属性上）。

## 运行测试

需要 Node >= 23.6（直接执行 TypeScript，类型擦除）：

```sh
npm test
```

测试覆盖：正常返回、恢复点注入、嵌套清理顺序、finally 内继续 yield、
finally 只执行一次、再次取消（新取消覆盖旧取消并以 `cause` 关联）、
清理异常覆盖原异常、return 穿越 try。
