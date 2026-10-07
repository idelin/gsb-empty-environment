# 磁盘格式与恢复说明

## 目录布局（扁平，所有原子替换在同一目录内完成）

```
writer.lock                 flock，单写进程互斥
nonce.log                   nonce 高水位预留记录
ops.log                     幂等索引（可由 manifest 重建）
obj-<hex(id)>-current       已提交版本索引（提交点）
obj-<hex(id)>-m<ver>-<h>.mf AEAD 加密的 manifest（内容寻址）
obj-<hex(id)>-f<fid>-r<rev>-<h>.ef  AEAD 加密的字段值（内容寻址）
*.tmp-*                     原子写的暂存文件，恢复时清除
```

对象 ID 经 hex 编码进文件名，任意字符串 ID 都安全；所有文件同目录，
一次目录 fsync 覆盖全部重命名。

## 通用编码

所有自描述结构使用无歧义的长度前缀编码：每个元素为 `uvarint(长度) || 字节`，
拼接是单射，不存在字段边界歧义。整数先转 8 字节大端再作为一个元素。

## 字段密文文件（.ef）

```
header:  "EF1" | keyID | nonce(12B)        （长度前缀编码）
body:    AES-GCM.Seal(nonce, plaintext, AD)
```

AD = `lenenc("encobj-field-v1", objID, fieldID, revision, schemaVersion, keyID)`，
绑定对象 ID、稳定字段 ID、字段修订号、schema 版本与 key ID。文件名
`f<fieldID>-r<revision>-<sha256(body)[:8]>.ef` 内容寻址；manifest 记录每个字段的
完整文件名与 SHA-256 摘要。头部不单独认证，但 keyID 与 nonce 同时是 AD/nonce
输入，篡改头部必然导致 AEAD 校验失败。

## manifest 文件（.mf）

```
header:  "EM1" | keyID | nonce(12B)
body:    AES-GCM.Seal(nonce, manifestPlaintext, AD)
AD = lenenc("encobj-manifest-v1", objID, version, schemaVersion, keyID)
```

manifestPlaintext（长度前缀编码，字段按名称排序，确定性）：

```
schemaVersion, version, keyID, opID, opDigest(32B), nextFieldID,
fieldCount, 每字段: {name, fieldID, revision, keyID, fileName, digest(32B)}
```

manifest 承诺：名称→稳定字段 ID 映射、当前字段集合、每个字段密文的摘要与
修订号、对象提交版本、幂等操作 ID 与内容摘要。仅单个字段认证成功不证明对象
完整——缺字段、多字段、密文交换、旧字段替换都被 manifest 承诺检出。

## current 记录（提交点）

```
lenenc("encobj-current-v1", version, manifestFileName, manifestDigest(32B)) || sha256(以上)
```

原子替换写入。读取对象时要求磁盘上的 current 与进程内已提交索引完全一致；
旧 manifest 的拒绝依据是此本地索引，而非 AEAD 推断的新旧。

## nonce.log

```
记录: crc32(reservedHi) || reservedHi(u64 LE)，严格递增
```

- 只有上界记录落盘（fsync）后，区间内 nonce 才可使用。
- 重启从最高已记录上界继续，跳过整个已预留区间（浪费，不复用）。
- 尾部半写（短记录或尾记录 crc 错）→ 以有效前缀原子重写截断；
  中段 crc 错或非递增 → 拒绝恢复（ErrCorrupt）。
- 计数达到 MaxNonce → ErrNonceExhausted，拒绝继续加密。

## ops.log

```
记录: crc32 || lenenc(version, opID, opDigest(32B))
```

恢复时校验、截断半写尾部、中段损坏拒绝恢复；随后用每个对象当前 manifest 内
的 `(opID, opDigest)` 补建索引（覆盖"已提交但 ops.log 未落盘"的崩溃窗口）。
同 ID 不同摘要 → ErrCorrupt。

## 恢复流程

1. 获取 writer.lock；读 nonce.log（截断/拒绝）。
2. 读 ops.log（截断/拒绝），装入幂等索引。
3. 删除所有 `*.tmp-*`。
4. 逐对象：读 current → 读 manifest 文件并核对摘要、AEAD、解码、版本/keyID
   自洽 → 核对每个字段文件存在、摘要匹配、AEAD 可开。任一失败拒绝启动。
5. 回收未被任何 current manifest 引用的 .ef/.mf（孤儿候选、失败轮换暂存、
   被注入的陌生文件），目录 fsync。
6. 用当前 manifest 的幂等记录补建索引。

恢复后只存在完整的旧对象或完整的新对象；未确认写入要么未提交、要么已完整
提交，绝不半提交。跨对象事务不在保证范围内。
