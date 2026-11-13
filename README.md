# largeBlob数组摘要与解压上限

凭据 largeBlob 存在认证器返回的序列化数组里，攻击者改数组或把 origSize 写得极大诱发解压炸弹。为TypeScript浏览器宿主写可导入的 largeBlob 读取包：尾部 16 字节须等于前文 SHA-256 的前 16 字节；条目用 AES-256-GCM 解密，附加数据为 "blob" 加小端 8 字节 origSize；解压长度须等于 origSize 且不超 1 MiB。

## 用法

```ts
import { readLargeBlob } from './index.ts';

// serialized：认证器返回的序列化数组；key：32 字节 AES-256 密钥或 AES-GCM CryptoKey
const blobs: Uint8Array[] = await readLargeBlob(serialized, key, { maxSize: 1 << 20 });
```

仅依赖 WebCrypto 与 `DecompressionStream`，可直接在浏览器宿主使用。任何校验失败都会抛出带 `code` 的 `LargeBlobError`。

## 序列化格式（整数均为小端）

```
body :=
  count         u32le           条目数
  count 个条目，每个条目 :=
    origSize    u64le           解压后明文长度（字节）
    cipherLen   u32le           密文长度（含 16 字节 GCM 认证标签）
    nonce       12 字节         AES-GCM IV
    ciphertext  cipherLen 字节  gzip(明文) 的 AES-256-GCM 密文
tag := SHA-256(body) 的前 16 字节，追加在 body 之后
```

每个条目的 AES-GCM 附加数据（AAD）为 `"blob"`（4 字节 ASCII）拼接 u64le 的 origSize，共 12 字节。

## 安全不变量

- 先校验尾部 16 字节摘要（SHA-256 前文取前 16 字节，常数时间比较），再解析与解密任何条目；
- origSize 超过 1 MiB（或调用方给定的更小 `maxSize`）直接拒绝，不解密、不解压；
- gzip 解压流式计数，输出一旦超过声明的 origSize 立即中止，抵御解压炸弹；
- 解压结果长度必须恰好等于 origSize；
- AAD 绑定 origSize，攻击者篡改头部尺寸字段会导致 GCM 认证失败。

## 测试

```sh
npm test
```
