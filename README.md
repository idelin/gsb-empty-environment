# chunkaead — 分段 AEAD 加密对象

面向对象存储网关的分段加密：大文件被切成固定大小的段，每段独立使用
AES-256-GCM 加密并认证，随机读（`io.ReaderAt`）只解密请求区间涉及的段。

## 存储格式

```
frame0 || frame1 || ... || frameN-1 || footer
```

- 每个 `frame` 是一段 GCM 密文，长度为 `chunkSize + 16`，末段可能更短。
- `footer`（位于对象尾部）：
  `baseNonce(12) || sealedHeader || u32BE(sealedHeaderLen) || "CHNKAED1"`。
- `sealedHeader` 是 GCM 加密的认证头，明文包含版本、`chunkSize`、
  对象明文总长度和对象标识（object ID）。

## Nonce 与 AAD 绑定

每个对象生成一个随机 96 位 `baseNonce`：

- 认证头使用派生 nonce 计数器 `0`；
- 第 `i` 段（从 0 开始）使用计数器 `i+1`，对 `baseNonce` 做 96 位
  大端加法派生，段内不存储 nonce。

每段的附加认证数据（AAD）为：

```
"CHNKAED1-seg-v1" || u16BE(objectIDLen) || objectID ||
u64BE(segmentIndex) || u32BE(plaintextLen) || finalFlag(1 字节)
```

由此：

- **段重排**：段号在 AAD 中，换位置后认证失败。
- **跨对象拼接**：对象标识在 AAD 中，别对象的帧无法通过本对象认证。
- **末段伪造/截断后补段**：末段标记与段长在 AAD 中。
- **整体截断/追加**：尾部认证头记录总明文长度与分段数，打开时按实际
  存储大小重算帧数并比对，不一致直接拒绝（`ErrTruncated`）。
- **空对象**：没有任何帧，只有认证尾头；打开后 `Size() == 0`，
  读取返回 `io.EOF`。

## 读取语义

`ReadAt(p, off)` 先把区间涉及的每一段都读入私有缓冲并完成 GCM 认证，
全部通过后才把明文复制进 `p`。任意一段认证失败时返回
`ErrAuthentication` 且 `n == 0`，调用方拿不到任何明文。`Reader` 无可变
状态，支持并发 `ReadAt`。

## 用法

```go
key := make([]byte, 32) // AES-256，调用方负责安全管理

// 写入
w, _ := chunkaead.NewWriter(backend, key, "bucket/object-key", 4<<20)
io.Copy(w, plaintextReader)
w.Close()

// 随机读
r, _ := chunkaead.NewReader(backendReaderAt, key, "bucket/object-key")
buf := make([]byte, 4096)
n, err := r.ReadAt(buf, someOffset)
```

后端需能报告大小：`*os.File`、`*bytes.Reader` 可直接使用；其他
`io.ReaderAt` 用 `chunkaead.NewSized(r, size)` 包装。
