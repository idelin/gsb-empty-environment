# Partition-scoped Web Locks

无依赖的 TypeScript 锁管理包，用于浏览器宿主。锁身份是以下元组的不可混淆编码：

```text
storagePartition + origin + lockName 的原始 UTF-8 字节 + hostPolicyGeneration
```

编码使用结构化 JSON 元组，锁名为十六进制字节串，因此分隔符、NUL、引号、非法
UTF-8 字节都不会造成跨字段碰撞。

## 使用

```ts
import {
  LockMode,
  ManualClock,
  WebLockManager,
} from "./index.ts";

const clock = new ManualClock(0);
const locks = new WebLockManager({ clock });

const lease = await locks.request({
  storagePartition: "top-level-site-a",
  origin: "https://app.example.test",
  lockName: "ledger",
  mode: LockMode.Exclusive,
  documentGeneration: 1,
  requestID: "r1",
  deadline: 100,
});

lease?.release();
```

回调形式中，只有成功授予后才会调用回调；回调完成/抛错自动释放 lease：

```ts
await locks.request(
  {
    storagePartition: "top-level-site-a",
    origin: "https://app.example.test",
    lockName: "ledger",
    documentGeneration: 1,
    requestID: "r2",
  },
  async (lease) => {
    // lease 已冻结，且绑定 documentGeneration 与随机 leaseID
    await doSensitiveWork(lease);
  },
);
```

`ifAvailable: true` 冲突时返回 `null`，不调用回调，也不写入等待队列：

```ts
const probe = await locks.request({
  // ...
  ifAvailable: true,
});
if (probe === null) {
  // 仅表示“本身份未立即获得”，不包含其他分区/源的任何状态
}
```

## 语义

- 每个身份维护单调请求序号和严格 FIFO 队列。
- `Exclusive` 与所有持有者冲突；`Shared` 只与 `Exclusive` 冲突。
- 队首连续 `Shared` 一次成批授予；后面的 `Exclusive` 不会越过该批次。
- 释放来源包括回调完成、`AbortSignal`、绝对截止时刻、导航和 host 策略撤销。
- 所有释放路径都经过同一个 lease 身份检查，重复释放不会再触发事件。
- 迟到回调持有的是旧 lease 对象，不能按锁名释放之后复用的同名新 lease。
- `query` 先按调用者的 `storagePartition`/`origin` 过滤，再限制到调用文档的
  `documentGeneration`；配置了 `canViewLock(viewer, lock)` 时由宿主作最终裁决。
  跨分区/源记录不会进入结果。
- `setHostPolicyGeneration(partition, next)` 立即拒绝旧代次的 Pending，并默认
  撤销旧代次的已授予 lease。可通过 `revokeGranted` 回调选择性保留。
- 随机 leaseID 在管理器生命周期内不复用；自定义 `createLeaseID` 返回重复值时
  会自动追加唯一后缀。

`ManualClock` 提供确定性的截止时刻调度：同一时刻的定时器按安装顺序执行，
`advance(ms)` 会同步运行所有到期回调。

## 安全轨迹

`test/security.test.ts` 覆盖：

- 在朴素 `[partition, origin, name].join("|")` 编码下会碰撞的两组输入；
- 非法 UTF-8、NUL、引号和代次差异；
- 两个顶层分区同名锁的阻塞与 `ifAvailable` 时序对照；
- 有界“最短轨迹”搜索：一个分区持有 Exclusive 时，另一分区仍立即获得同名锁，
  未找到跨分区阻塞或时序观察分支。

## 测试

```sh
npm test
```

测试仅使用 Node 内置的 `node:test` 与类型剥离，不需要安装依赖。
