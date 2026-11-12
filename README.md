# 沙箱 Blob 地址授权注册包

安全沙箱里的 Blob 地址只是一段可复制的字符串，**持有地址不等于拥有读取权**。
本包是一个可直接导入的 TypeScript 地址注册表，把每个地址绑定到创建时的
安全来源（origin）、存储分区（storage partition，租户边界）和实例代次
（instance generation），读取前先核验调用文档；撤销只阻断新读取，已签发的
流按自己的既定租约自然结束。

## 安全模型

- **四维绑定**：地址记录 `creatorOrigin + partition + generation + creatorDocumentId`。
  `openRead` 时调用文档必须仍处于 active 状态，且来源、分区、代次三项全部匹配。
- **字符串复制不授权**：别的分区、来源或代次拿到同一个 `blob:` URL 只会得到
  统一的 `AddressDeniedError`，无法区分地址不存在、已撤销还是属于其他租户，
  因此不能用来探测他人地址空间。
- **撤销语义**：`revoke` 之后新读取失败（属主得到 `AddressRevokedError`，
  其他租户仍得到 `AddressDeniedError`）；撤销前已取得的 `LeasedReadStream`
  不受影响，按租约到期、读完或显式取消三种方式之一结束。
- **导航语义**：`BrowsingContext.navigate()` 会停用旧文档并中止其生命周期
  信号，挂起中的读取在流签发前被取消；同来源同分区导航后的新文档仍满足
  绑定，跨来源/跨分区导航则被拒绝。
- **实例代次**：新代次文档无法读取旧代次地址；`purgeGeneration()` 模拟沙箱
  实例重启后注册表中旧地址彻底消失。
- **数据快照**：创建地址时复制字节，调用方事后修改缓冲区不影响已注册内容。

## 目录结构

```
src/
  errors.ts        统一错误类型（拒绝/不存在/已撤销/中止/已关闭）
  types.ts         来源、分区、代次、文档与绑定的接口定义
  context.ts       SandboxDocument 与 BrowsingContext（导航/关闭生命周期）
  leased-stream.ts 固定租约的分块读取流，支持取消与异步迭代
  registry.ts      BlobAddressRegistry 注册、核验、撤销与事件
  index.ts         包导出入口
test/              授权、撤销、地址复用、导航、取消、租约共 36 个测试
```

## 使用示例

```ts
import {
  BlobAddressRegistry,
  BrowsingContext,
  SandboxDocument,
} from "./index.ts";

const registry = new BlobAddressRegistry({ defaultLeaseMs: 5_000 });
const browsing = new BrowsingContext(
  new SandboxDocument(
    { scheme: "https", host: "app.example", port: null },
    { id: "partition-a" },
    { id: "instance-1" },
  ),
);

const binding = registry.createAddress({
  document: browsing.document,
  data: new TextEncoder().encode("tenant-secret"),
  contentType: "text/plain",
});

// 授权文档读取
const stream = await registry.openRead(binding.url, {
  document: browsing.document,
});
for await (const chunk of stream) {
  // 分块消费；break 会以 cancelled 结束租约
}

// 导航：旧文档停用，挂起读取被中止
browsing.navigate({ partition: { id: "partition-b" } });

// 撤销：新读取失败，已签发流继续到租约结束
registry.revoke(binding.url, { document: browsing.document });
```

## 错误处理

所有失败都抛出 `AddressError` 子类，按 `code` 分支处理：

| code | 含义 |
| --- | --- |
| `ADDRESS_DENIED` | 文档 inactive、来源/分区/代次不匹配，或 URL 非法 |
| `ADDRESS_NOT_FOUND` | 属主视角下地址不存在 |
| `ADDRESS_REVOKED` | 属主视角下地址已撤销 |
| `READ_ABORTED` | 流签发前信号被中止，或已签发流被取消 |
| `READ_CLOSED` | 租约过期或流已结束后继续读取 |

## 运行测试

需要 Node ≥ 22.6（使用内置 `--experimental-strip-types` 直接运行 TS）：

```bash
node --experimental-strip-types --test 'test/**/*.test.ts'
```

测试覆盖：跨分区/跨来源/跨代次授权矩阵、地址复用不复活旧内容、撤销后
新读失败而旧流存活、同/跨租户导航交错、读取中止与取消、租约到期与综合交错场景。
