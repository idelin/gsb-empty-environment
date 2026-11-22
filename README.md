# 认证资源宿主流水线

这个包提供一套可直接导入的 TypeScript 认证流水线，覆盖：

- 传输表示与解码后表示分别认证（域标签、长度、摘要组）。
- 多个独立摘要组；每组取“最强受支持算法”，弱摘要不能掩盖强摘要不符。
- 传输字节摘要不会用于认证解码字节。
- 分片乱序、重复、冲突检测，以及解码器零消费/待取输出、背压和取消。
- 清单声明位置上的请求身份绑定占位替换。
- 绑定变换链、内容承诺、请求主体、响应类型的验证缓存。
- 当前撤销集重核，包括共享分片撤销。
- 验证完成前不可见的读取回调，以及原子发布凭证。

## 快速开始

```ts
import {
  AuthenticatedAssetHost,
  VerificationCache,
  RevocationSet,
  createManifest,
  encodeDeterministic,
} from "./src/index.ts";

const decoded = new TextEncoder().encode("hello __REQ__");
const token = new TextEncoder().encode("__REQ__");
const transport = encodeDeterministic(decoded);

const manifest = createManifest({
  transportBytes: transport,
  decodedBytes: decoded,
  transport: {
    domain: "cdn.transport.v1",
    groups: [["sha256"], ["sha512"]],
  },
  decoded: {
    domain: "decoded.v1",
    algorithms: ["sha256", "sha512"],
  },
  transform: ["rle"],
  placeholders: [
    {
      id: "request",
      offset: 6,
      length: token.byteLength,
      token,
      field: "requestId",
    },
  ],
});

const host = new AuthenticatedAssetHost({
  cache: new VerificationCache(),
  revocations: new RevocationSet(),
});

const asset = await host.publish({
  manifest,
  identity: {
    requestId: "request-42",
    body: "request body",
    responseType: "text",
  },
  shards: [[0, transport]],
});

const bytes = asset.credential.read();
```

也可以逐片驱动：

```ts
const pipeline = host.createPipeline(manifest, {
  identity: { requestId: "request-42" },
});
pipeline.onRead((credential) => {
  // 只有全部摘要组和撤销检查通过后才会执行。
  console.log(credential.read());
});
pipeline.addShard(1, transport.slice(10));
pipeline.addShard(0, transport.slice(0, 10));
const published = await pipeline.end();
```

## 清单

`normalizeManifest` 接受多种常见字段别名。核心形状：

```ts
interface Manifest {
  transport: Representation;
  decoded: Representation;
  final?: Representation;        // 可选的替换后字节表示
  decodedStage?: "auto" | "raw" | "final";
  transformChain?: TransformStep[] | string[];
  placeholders?: PlaceholderSpec[];
  shards?: { count?: number; lengths?: number[]; shards?: ShardSpec[] };
  contentCommitment?: string;
}

interface Representation {
  domain: string;
  length: number;
  algorithm?: string;
  digest?: string;
  algorithms?: string[];
  digests?: string[] | DigestEntry[] | Record<string, string>;
  groups?: DigestGroup[];
}

interface DigestGroup {
  id?: string;
  domain?: string;
  length?: number;
  entries: { algorithm: string; digest: string; domain?: string }[];
}
```

`decodedStage` 默认 `"auto"`：先尝试用解码后表示的摘要认证原始解码字节；若不匹配，再对占位替换后的最终字节认证。`"raw"` 只认证替换前字节，`"final"` 只认证替换后字节。若声明了 `final` 表示，最终字节还会额外认证。

### 摘要组语义

- 每个组独立验证，所有组都必须通过。
- 组内先选最强且当前运行时支持的算法（`sha512 > sha384 > sha256 > sha1 > md5`）。
- 只检查该算法下的摘要；同组弱算法即使匹配也不能让强算法不匹配通过。
- 不支持的未来算法会被忽略，不会阻止受支持算法回退。
- `transport` 组只用于传输字节，`decoded`/`final` 组只用于对应解码阶段字节。

摘要输入使用长度前缀域分离：

```text
SHA("GSB-AUTH-V1\n" || u32be(len(domain)) || domain
    || content || u64be(len(content)))
```

`StreamingHasher`、`digestHex`、`buildRepresentation` 都使用同一编码。

## 占位替换

`placeholders` 的 `offset`/`length` 是解码流中的源区间，替换只会发生在这里：

- `token` 可选；声明后源区间必须逐字节匹配。
- `field` 从请求身份读取值（支持 `requestId`、`id`、`headers.x-...` 等路径）。
- 也可通过 `identity.placeholderValues[id]` 提供值。
- `bindingDigest` 可由 `computePlaceholderBinding(identity, spec, value)` 生成；不匹配的请求身份会被拒绝。
- 替换只做一遍，值中再次出现占位符不会再被替换。

## 分片、解码与背压

- `addShard(index, data)` 接受乱序分片。
- 相同索引、相同字节的重复分片会被忽略；相同索引、不同字节立即 `SHARD_CONFLICT`。
- `pipeline.addShard(...)` 返回 `backpressured`；异步 sink 场景可直接使用 `driveDecoder()`，它会在消费零字节但有待取输出时先排空输出再重试。
- `cancel(reason)` 丢弃全部数据；`disconnect(reason)` 只在对象已经完整并验证通过时返回凭证，否则不提交任何部分对象。
- 内置确定性小解码器格式：

```text
0x01 u16be length  literal bytes
0x02 u16be length  repeated byte
0x00               optional end marker
```

空 literal/repeat 块合法；不完整块、尾随字节、非法控制字节都会失败。

## 缓存与撤销

`VerificationCache` 的身份键绑定：

- 变换链（`TransformStep[]`）
- 内容承诺
- 请求主体（原文或摘要）
- 响应类型
- 占位绑定摘要、清单指纹、可选实例 ID

完整已验证对象按身份键缓存。跨身份只能通过 `acquireIntermediate` 复用**尚未实例化**的已验证中间数据；一旦被某个身份实例化，其他身份不能复用。

每个缓存记录保存撤销引用（传输/解码摘要、内容承诺、分片索引与分片摘要）。`cache.get(key, revocations)` 每次命中都会重核当前 `RevocationSet`，共享分片被撤销时所有引用它的记录都会失效。

## 发布凭证

`PublicationGate` 在 `commit(proof, bytes)` 前不会向任何读取回调暴露字节。`PublicationCredential` 只能由门禁在验证通过后签发：

- `credential.read(offset, length)` 返回副本。
- 读取时会检查当前撤销集。
- `pipeline.read()`、`gate.read()` 在发布前抛 `NOT_PUBLISHED`。
- `disconnect()` 已提交时返回同一凭证；未完成时直接丢弃。

## 测试

```bash
npm test
```

测试覆盖摘要组强弱冲突、域/长度分离、所有切点、空尾块、零消费输出、背压、取消、占位绑定、迟到回调、断线原子提交、缓存命中撤销与共享分片撤销。
