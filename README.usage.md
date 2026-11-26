# 使用说明

## 运行测试与验收

```bash
# 37 个单元/参考测试（Node 内置 test runner，零依赖、零联网）
node --experimental-strip-types --test "test/**/*.test.ts"

# 只读验收包：输出结构化报告到 stdout，退出码反映成败
node --experimental-strip-types acceptance/run-acceptance.ts

# 可选：把验收报告写到专属临时目录
node --experimental-strip-types acceptance/run-acceptance.ts --out "$TMPDIR/acceptance.json"

# 模块加载与契约探针
node --experimental-strip-types scripts/typecheck-probe.ts
```

## 最小集成示例

```ts
import {
  InMemoryContentStore,
  buildManifest,
  contentIdOf,
  exportPublicKeySpki,
  generateRootKeyPair,
  resolveContext,
  signManifest,
  verifyManifest,
} from "./src/index.ts";

const store = new InMemoryContentStore();
const context = {
  "@vocab": "https://cred.test/#",
  type: { "@id": "https://cred.test/#type", "@protected": true },
};
const cid = store.ingest(context);

const keyPair = await generateRootKeyPair();
const spki = await exportPublicKeySpki(keyPair.publicKey);
const manifest = buildManifest("root-1", spki, [{ cid }], "2026-11-26T00:00:00Z");
const envelope = await signManifest(manifest, keyPair.privateKey);
const { allowed } = await verifyManifest(envelope);

const resolution = resolveContext({ rootCid: cid, store, allowed });
```

## 目录

- `src/types.ts`：类型模型与错误码、诊断契约。
- `src/canonical.ts` / `src/content-address.ts`：确定性规范化与内容寻址。
- `src/trust-manifest.ts`：Ed25519 清单签名、验签与白名单闸门。
- `src/context-resolver.ts` / `src/engine-state.ts` / `src/term-definition.ts`：
  导入链、传播、嵌套、受保护碰撞引擎。
- `src/cache/semantic-cache.ts`：语义缓存键、健全性核验与来源闭包。
- `src/small-graph.ts`：无缓存小图参考扩展器。
- `acceptance/`：只读验收包与离线夹具。
- `test/`：信任、引擎、缓存与小图参考测试。
- `docs/security-model.md`：安全模型与威胁处置说明。
