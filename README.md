# ringlog — 固定容量磁盘环形日志 (C++17)

## 特性

- **定长容量**: 数据区大小固定, 空间不足时覆盖最旧记录。
- **变长记录 + padding**: 记录 = 24 字节头 + 变长负载, 8 字节对齐;
  物理末尾剩余空间放不下整条记录时写入 padding 记录并绕回到起点。
- **全局递增序号**: 每条记录携带单调递增的 64 位序号, 重启后延续。
- **覆盖检测**: 读游标只保存序号 (`Cursor{next_seq}`), 读取时从 head
  按序号扫描定位; 序号小于 `headSeq()` 即返回 `Overwritten` ——
  新一圈复用同一物理偏移的记录永远不会被当成旧记录返回。
- **撕裂写安全**: 提交协议为
  1. 推进 head 并先落盘元数据 (防止未提交写覆盖已提交数据);
  2. 写 padding + 记录 (单次 pwrite), fsync;
  3. 写元数据提交 tail, fsync。
  恢复时从头扫描已提交区域, 校验 magic/长度/序号/CRC32,
  第一个无效条目即视为撕裂尾部并截断 —— 只影响未提交的尾部。
- **元数据双副本**: 两个元数据槽 (偏移 0 / 4096), 各含 CRC32 与代际号,
  交替写入; 恢复时取校验通过且代际最新的一份, 双份均损坏则拒绝打开。

## 文件布局

```
[0, 4096)     元数据槽 0 (64 字节, CRC32 校验)
[4096, 8192)  元数据槽 1
[8192, ...)   数据区, 固定 capacity 字节
```

记录头 (24 字节): `magic | payload_len | seq | crc32 | reserved`,
CRC 覆盖头部前 16 字节 + 负载。padding 头 (8 字节): `magic | len`。

## API 摘要

```cpp
ringlog::RingLog log(path, {/*.capacity =*/ 1 << 20, /*.sync =*/ true});
uint64_t seq;
log.append("payload", &seq);            // Status::Ok / TooLarge / IOError
ringlog::Cursor cur = log.oldestCursor();
ringlog::Record rec;
log.read(cur, rec);                     // Ok / End / Overwritten / Corrupt
```

## 构建与测试

```sh
make test
```

测试覆盖 (`test_ringlog.cpp`):

- 基本读写与重启恢复 (序号跨重启延续);
- **恰好填满**: 容量整除记录大小, 写满后全部可读, 再写一条仅逐出最旧记录;
- **padding**: 构造末尾剩余 8 字节场景, 验证 padding 落盘与绕回;
- **小容量穷举绕回**: 容量 {128, 256, 512, 2048} × 4 随机种子 × 1500 次
  随机长度追加, 每次追加后与模型比对全部存活记录, 并穿插中途恢复;
- **新圈同偏移**: 旧游标读到的是 `Overwritten` 而非新圈记录;
- **撕裂写**: 在尾部直接 pwrite 截断记录 / 坏 CRC 垃圾, 恢复后已提交
  记录完整、尾部被丢弃、日志可继续追加;
- **元数据撕裂**: 损坏最新槽 → 回退到旧槽; 损坏旧槽 → 无影响;
  双槽均损坏 → 打开失败。
