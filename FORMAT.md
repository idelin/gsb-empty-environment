# 存储格式与崩溃恢复协议说明

## 范围声明

本项目的故障注入**仅模拟**:单个操作(写入 / 同步 / 重命名 / 目录同步 / 删除)的成功或失败、
日志追加的字节前缀截断(torn append),以及进程在任意步骤退出后的重启。
**不宣称模拟**真实断电、硬件写重排或物理扇区撕裂。

## 磁盘布局

```
<store>/
  MANIFEST.LOG            提交日志(唯一的事实来源)
  segments/
    seg-<16hex>.seg       不可变数据段,内容寻址(ID = 内容 SHA-256 前 8 字节)
    .tmp-<16hex>.seg      发布过程中的临时段文件(崩溃残留,GC 清理)
```

## 日志帧格式

`MANIFEST.LOG` 是帧的序列,每帧:

```
| len: uint32 BE | payload: len 字节 (JSON Record) | crc32: uint32 BE (IEEE, 对 payload) |
```

单帧上限 32 MiB,超出视为损坏。

### Record(JSON)

```json
{
  "version": 3,                  // 版本号,从 1 开始严格连续递增
  "prev_digest": "491ae0a1…",    // 前一帧 payload 的 SHA-256(十六进制);v1 为空串
  "added":   [{"id":"seg-…","digest":"…","size":13}],   // 本版本新增段
  "removed": ["seg-…"],                                   // 本版本移除段 ID
  "segments":[{"id":"seg-…","digest":"…","size":13}]      // 本版本完整段表(按 ID 排序)
}
```

- `digest` 为段文件内容的 SHA-256(十六进制)。
- 版本链双重校验:`version` 连续递增,且 `prev_digest` 逐帧链接;
  另校验 `segments` 全表必须等于"上一版段表 + added − removed"的推演结果。

## 发布协议(提交步骤与退出点)

`Publish(adds, removes)` 按序执行,每步之间都是可能的进程退出点:

| 步骤 | 操作 | 说明 |
|------|------|------|
| 1 | write  | 每个新段写入 `segments/.tmp-<id>.seg` |
| 2 | sync   | fsync 临时段文件 |
| 3 | verify | 读回并校验 SHA-256;**只有校验通过的段才允许被新版本引用** |
| 4 | rename | 原子重命名为 `seg-<id>.seg` |
| 5 | dirsync| fsync `segments/` 目录 |
| 6 | append | 向 `MANIFEST.LOG` 追加提交帧 |
| 7 | sync   | fsync 日志文件 —— **提交点** |
| 8 | dirsync| fsync 根目录 |

不变式:**提交点之前崩溃 → 旧版本完整可见;提交点之后崩溃 → 新版本整套可见。
不存在中间可见状态。** 段数据永远先于引用它的提交记录落盘(步骤 1–5 先于 6–8)。

## 打开时恢复(Open)

1. 顺序解析日志帧:
   - **不完整末条**(长度字段残缺 / payload 或 CRC 不足):容忍,截断日志到最后的完好偏移并 fsync;
   - **完整坏条**(CRC 不匹配、长度越界、JSON 无法解析):报错,拒绝打开。
2. 校验版本链连续递增、`prev_digest` 链接、段表与 added/removed 增量一致。
3. 校验当前版本引用的每个段:文件存在、大小与 SHA-256 摘要匹配;缺失或不匹配即报错。

## 垃圾回收(GC)

- 保留集合 = 当前版本的段 ∪ 所有活动读快照( pinning 版本)的段。
- 删除:不在保留集合中的 `seg-*.seg`、以及崩溃残留的 `.tmp-*` 临时文件。
- **未知文件**(不匹配段命名规则):识别并列在 `GCReport.Orphans` 中,
  既不删除,也**不会被误认为有效段**。
- 回收器在任何情况下都不删除活动快照或当前版本引用的文件。

## 测试覆盖的退出点枚举

`TestCrashAtEveryPublishStep` 先测量一次发布包含的可变操作数 N(本实现为 7:
write/sync/rename/dirsync/append/sync/dirsync),再对 0..N 每个退出点注入崩溃,
从磁盘状态恢复后核对:无半提交版本、旧版本数据完好、共享段未被误删、GC 后存储仍可通过完整校验。
