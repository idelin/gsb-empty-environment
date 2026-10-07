# async-di-container

TypeScript 异步单例依赖注入容器。工厂可通过 `resolver` 异步请求其他服务。

## 特性

- **请求链环检测**:沿单次请求的解析链(经 `AsyncLocalStorage` 传播)检测环,
  并以 `a -> b -> c -> a` 形式输出闭环(`DependencyCycleError.chain`)。
- **并发环检测**:跨独立并发请求用等待图(wait-for graph)检测互相等待造成的环,
  两个请求都会以 `DependencyCycleError` 拒绝,不会死锁。
- **构造共享**:同一单例的并发请求共享同一次构造(同一个 Promise / 实例)。
- **失败重试**:构造失败的一代被丢弃并回到 idle,后续请求开启新一代重试;
  同一代的并发等待者共享同一次失败。
- **逆序释放**:`dispose()` 按构造完成的逆序(即逆依赖顺序)逐个 await 释放,
  幂等且每个资源恰释放一次;构造失败的资源不参与释放。

## 运行

无需安装依赖(Node >= 23.6 原生擦除类型运行 `.ts`):

```sh
npm test
```

## 用法

```ts
import { Container } from "./src/container.ts";

const container = new Container();
container.register("db", async () => connectDb(), (db) => db.close());
container.register("repo", async (r) => new Repo(await r.get("db")));

const repo = await container.get("repo");
await container.dispose(); // 先释放 repo,再关闭 db
```
