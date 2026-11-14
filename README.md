# WebGPU buffer mapping lifecycle

一个供 WebGPU 宿主/GPU 进程使用的 TypeScript 缓冲映射生命周期组件。它把脚本可见的
`GPUBuffer` 与真实 GPU allocation 分离，并为每次映射请求绑定完整的资源身份，避免
`destroy()` 后槽位复用时的旧回调、旧 Promise 或旧 `ArrayBuffer` 访问新页面资源。

## 快速开始

```ts
import {
  GPUBufferUsage,
  GPUMapMode,
  PausableGpuCallbacks,
  WebGpuBufferMappingHost,
} from "./index.ts";

const callbacks = new PausableGpuCallbacks();
const host = new WebGpuBufferMappingHost({
  origin: "https://example.com",
  callbacks,
});
const device = host.createDevice();
const buffer = device.createBuffer({
  size: 64,
  usage: GPUBufferUsage.MAP_WRITE | GPUBufferUsage.COPY_SRC,
});

const mapDone = buffer.mapAsync(GPUMapMode.WRITE, 0, 64);
callbacks.completeNext();
await mapDone;

const bytes = new Uint8Array(buffer.getMappedRange());
bytes.set([1, 2, 3, 4]);
buffer.unmap();
```

默认的 `PausableGpuCallbacks` 不会自动完成回调：宿主或测试通过
`completeNext()`, `complete(id)`, `completeAll()` 或 `failNext()` 驱动 GPU 回调。
也可以传入实现 `GpuMapCallbackScheduler` 的自定义队列。

## 资源身份

每个 `GpuBuffer.identity` 都包含：

- 规范化后的 `origin` 与 host `partitionId` 隔离分区；
- `deviceId`, 创建时的 `deviceEpoch` 和 device `timeline`；
- buffer `slot`, `generation`, `allocationId`。

`generation` 与 `promiseGeneration` 由 origin 分区独立、单调递增。不同 origin 的
host 各自维护 generation 空间；即使 slot 和 generation 数值碰巧相同，identity 中的
origin/device/allocation 也不同。一个 `GpuDevice` 只能用于创建它的 host，跨 origin
传入 device 会被拒绝。

导航会递增 `navigationEpoch`，策略撤销会递增 `policyEpoch`。`mapAsync` 在提交时保存
两个 epoch、device-lost epoch、提交时的 device timeline，以及请求模式与范围。

## 请求验证

`mapAsync(mode, offset?, size?)` 在进入 `pending` 前检查：

- `offset` 是 8 的倍数，总范围大小是 4 的倍数；
- 使用 `offset > limit - size` 比较，避免 `offset + size` 溢出；
- 范围在 buffer 内，`size` 省略时映射剩余范围；
- `READ` 需要 `MAP_READ` usage，`WRITE` 需要 `MAP_WRITE` usage；
- `MAP_READ` 只能额外配 `COPY_DST`，`MAP_WRITE` 只能额外配 `COPY_SRC`；
- 同一 buffer 处于 `pending` 或 `mapped` 时拒绝新映射，包括重叠请求。

`getMappedRange(offset?, size?)` 使用相对于当前映射起点的相同对齐规则，并要求范围
位于当前映射内、不与任何仍活动的 `getMappedRange` 重叠。

## 状态机

```text
unmapped --mapAsync(valid)--> pending --GPU success--> mapped
    ^                            |                       |
    |                            +--GPU failure----------+
    |                            |                       |
    +------ unmap/cancel --------+---- unmap ------------+

pending/mapped --destroy/loss/navigation/policy--> unmapped + detached + invalid
```

- GPU success 只在 resource 仍为同一 generation/allocation、状态仍为 `pending`、
  device epoch 与提交时一致、navigation/policy epoch 未变化时发布映射。
- `destroy()`、`unmap()`、device loss、navigation、policy revocation 会同步取消
  请求、拒绝待定 Promise，并使所有 `getMappedRange` 返回的 `ArrayBuffer` detached。
- `destroy()` 会立即使 slot 可复用，但旧 allocation 在所有迟到回调返回前保持
  `retired`；迟到 success 只递减旧 allocation 的 outstanding callback 并释放
  旧 allocation，绝不会触碰复用 slot 的新 allocation。
- `unmap()` 是幂等的。对 `pending` 调用会取消请求；对已 `mapped` 调用会提交
  write 数据、detach 视图并回到 `unmapped`；再次调用不变。
- `mappedAtCreation: true` 创建即为 `mapped`，可立即 `getMappedRange()`；`unmap()`
  后按普通 buffer 状态机运行。`mappedAtCreation` 要求 size 为 4 的倍数。
- `MAP_WRITE` 映射在 `unmap()` 时提交数据；`MAP_READ` 映射不提交脚本写入。
- device loss 后旧 resources 全部失效；同一 device 可 `recover()`，epoch 会递增，
  但必须创建新 buffer。navigation/policy 会 retire device，之后必须创建新 device。

## 审计与测试

`host.getAllocationAudit()` 返回每个 allocation 的 `live`, `retired` 或 `released`
状态、outstanding callback 数、创建/retire/release timeline。它可用于断言迟到回调
只回收旧 allocation。

```bash
npm test
```

测试使用可暂停 GPU 回调专门交错：

- `destroy()` 后复用 slot，再执行旧回调；
- detached 视图销毁后重建；
- device loss 后恢复并复用槽位；
- navigation 与 policy revocation 后重建；
- pending `unmap()`、重复 `unmap()`、MappedAtCreation；
- 范围边界、遗漏 size、零长度映射、溢出、`getMappedRange` 重叠；
- read-only/write-only usage、callback failure、跨 origin 独立 generation；
- 旧 Promise/旧视图无法访问复用 allocation。
