# async-di-container

异步单例服务容器:工厂可在构造中通过 `resolver.get()` 异步请求其他服务。

## 语义

- **依赖环检测**:按请求链(AsyncLocalStorage 传递的构造栈)检测同链环;对跨并发请求链的环,在构造记录上维护 `waitingOn` 等待边,请求他人正在构造的服务时沿等待图回走,命中自身即抛出 `CycleError`,消息输出闭环(如 `a -> b -> a`)。
- **共享构造**:同一服务的并发请求共享同一代(in-flight)构造 Promise,工厂只执行一次。
- **失败重试**:构造失败后代(generation)复位为 `idle`,所有等待者收到同一拒绝;后续 `get` 启动新一代重新构造。
- **逆序释放**:`dispose()` 按构造完成的逆序(即逆依赖顺序)逐个调用注册的 `dispose`,每个实例恰清理一次;失败的代不产生清理。

## 运行测试

需要 Node.js >= 23.6(原生类型擦除运行 TypeScript):

```sh
node --test test/*.test.ts   # 或 npm test
```

## 示例

```ts
import { Container } from './src/container.ts';

const container = new Container();
container.register('db', {
  factory: async () => connect(),
  dispose: async (db) => db.close(),
});
container.register('repo', {
  factory: async (r) => new Repo(await r.get('db')),
});

const repo = await container.get('repo');
await container.dispose(); // repo 先于 db 释放
```

## 文件

- `src/container.ts` — 容器实现(环检测、代管理、释放)
- `src/barrier.ts` — 测试用屏障工具
- `test/container.test.ts` — 屏障编排的并发环、失败恢复、恰一次清理等用例
