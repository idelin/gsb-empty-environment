# ringlog

固定容量的磁盘环形日志库（C++17），单文件存储，变长记录，序号全局递增。

## 文件布局

```
[0, 4096)        元数据副本 A：magic, version, capacity, used,
                 head_off/head_seq, tail_off/tail_seq, generation, crc32
[4096, 8192)     元数据副本 B（同格式）
[8192, 8192+cap) 环形数据区：记录头(24B) + 变长负载，padding 记录填充物理末尾
```

记录头：`magic(4) | len(4) | seq(8) | crc32(4) | reserved(4)`，
crc 覆盖 `len+seq+payload`。padding 记录的 `len` 字段为含头总跨度；
物理末尾不足一个记录头的残余字节由读写双方按隐式 gap 跳过。

## 一致性设计

- **不跨物理末尾**：记录放不下时先写 padding 再绕回偏移 0，记录本身永不跨界。
- **全局递增序号**：`tail_seq` 持久化在元数据中，重开后续增；每条记录携带
  自己的 seq，读游标按 seq 精确匹配——同一偏移被新圈复用后 seq 必然更大，
  配合 crc 校验，绝不会把新数据误当旧记录（返回 `kOverwritten` 并自动
  重同步到当前最旧存活记录）。
- **撕裂只影响未提交尾部**：先写数据并 fsync，再提交元数据；崩溃只会在
  已提交 tail 之外留下垃圾，读者不可达，下次写入直接覆盖。
- **元数据双副本**：两份独立写入、各自 fsync，带 crc 与递增 generation；
  打开时取合法且较新的一份，并自动修复损坏的另一份；双份皆坏则打开失败。

## 构建与测试

```sh
cmake -S . -B build && cmake --build build
./build/ringlog_test        # 或 ctest --test-dir build
```

测试覆盖：小容量穷举绕回（多容量 × 多记录长 × 多圈，模型对拍）、恰好填满、
padding 绕回、覆盖检测、崩溃恢复后续写、元数据单副本损坏修复与双副本损坏
失败、未提交尾部垃圾容忍、随机模型测试（含周期性重开）。
