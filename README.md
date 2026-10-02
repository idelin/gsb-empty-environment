# 基于租约的分布式缓存失效一致性保证器

TypeScript 实现的 AFS/Leases 风格一致性协议:客户端凭**读租约**放心读本地缓存;
写者修改文件前,服务端先撤销全部有效租约,持有者失联则**安全阻塞至租约期满**,
保证**零读脏**。

## 环境要求

- Node.js ≥ 22.18 或 ≥ 23.6(内置 TypeScript 类型擦除,无需编译步骤)
- 可选:`npm i -D typescript` 后 `npm run typecheck` 做类型检查

## 运行

```bash
npm test    # node --test 'test/*.test.ts'
npm run demo # 验收场景演示(真实 5 秒租约)
```

## 协议要点

- `LeaseServer.acquireReadLease(file, clientId)`:授予有时限的读租约并返回内容。
- `CacheClient.read(file)`:租约有效期内直接返回本地缓存,不访问服务端。
- `LeaseServer.write(file, writer, content)`:
  1. 找出该文件所有他人持有的有效租约;
  2. 并发调用各持有者注册的撤销回调(AFS callback);
  3. 回调确认 → 立即放行;回调不可达/超时 → 阻塞至该租约过期时刻;
  4. 全部租约失效后才落盘(落盘前有防御性校验)。
- 同一文件的写操作通过 Promise 链串行化。

## 验收场景(见 `npm run demo`)

1. t=0s:客户端1 获得文件 X 的 5s 读租约,随后失联;
2. t=2s:客户端2 请求写 X,服务端通知撤销失败,安全阻塞;
3. t=3s:客户端1 读本地缓存仍为旧值,与服务端一致(零读脏);
4. t=5s:租约期满,服务端放行写入;新读者读到新值。

## 文件结构

- `src/lease-server.ts` — 服务端:租约授予、撤销通知、阻塞写、写串行化
- `src/cache-client.ts` — 客户端:本地缓存、撤销回调、失联/重连模拟
- `test/lease.test.ts` — Node.js 内置测试(`node:test`),6 个用例
- `examples/demo.ts` — 验收场景的真实时间演示
