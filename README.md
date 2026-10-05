# gsb-random-stream

按树状测试名称派生子流的确定性随机流库（TypeScript）。

## 设计

- **派生**：每条流持有一个 256 位密钥。
  - 根流：`rootKey = SHA-256(frame([TAG_ROOT, seed]))`
  - 子流：`childKey = SHA-256(frame([TAG_CHILD, parentKey, segment]))`
  - `frame` 为长度前缀帧编码（`4 字节大端长度 || 内容`），是单射，
    任意路径与字节串一一对应。
- **输出**：计数器模式，第 i 块为
  `SHA-256(frame([TAG_DRAW, key, counter_i]))`（计数器 128 位大端）。
  输出是 `(密钥, 位置)` 的纯函数，因此**分批生成与一次生成必然等价**。
- **无宿主随机源**：不使用 `Math.random`、`crypto.getRandomValues`、
  `node:crypto` 等任何宿主 API；SHA-256 为纯 TS 实现，全程整数运算，
  跨平台逐位一致。

## 性质

- 新增兄弟测试不改变已有测试的数据（`child()` 是纯函数，不触碰父流）。
- 重命名测试即得到全新的流（流由名称路径决定）。
- 相同种子 + 相同名称路径，在任何平台上得到逐位相同的序列。

## 名称规范与歧义拒绝

名称段在派生前经规范编码校验，以下情况抛出 `NameEncodingError`：

- 空段；
- 含路径分隔符 `/` 或 NUL 字符；
- 非 NFC 规范形式（如 `e` + 组合音符 与 预组合 `é` 这类显示相同、
  码点不同的写法，静默归一化会造成两条不同名称共享一条流，故拒绝）；
- 孤立代理项（UTF-8 编码会替换为 U+FFFD 而产生碰撞）；
- 超过 4096 字节。

## 用法

```ts
import { RandomStream } from "./src/index.ts";

const root = RandomStream.fromSeed("pairwise-gsb");
const stream = root.child("套件").child("用例-一");

stream.nextBytes(32);   // Uint8Array
stream.nextUint32();    // 无符号 32 位整数
stream.nextFloat();     // [0, 1)，53 位精度
stream.nextInt(1, 7);   // [1, 7)，拒绝采样无模偏倚
```

## 测试

需要 Node.js >= 22.6（利用原生 TypeScript 类型擦除运行，无需构建）：

```sh
npm test
```

测试覆盖：

- `test/sha256.test.ts`：FIPS 180-4 标准向量；
- `test/encoding.test.ts`：规范编码字节级向量与全部歧义拒绝路径；
- `test/tree.test.ts`：兄弟独立、重命名换流、派生纯度、层级区分；
- `test/consistency.test.ts`：分批与一次生成等价、混合类型游标一致；
- `test/vectors.test.ts`：跨平台固定向量（算法变更时必须整体重新生成）。

如需类型检查，安装 dev 依赖后运行 `npm run typecheck`
（`typescript` 与 `@types/node`）。
