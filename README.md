# XDP元数据可信标签

XDP 程序把判定结果写进数据包前的元数据区，后级 TC 或 AF_XDP 消费者据此信任流量，攻击者可借转发路径带入伪造的元数据。为C++17数据面写供两端共用的元数据读写库：布局为魔数、版本、写入阶段编号、4 字节对齐的负载和以每次启动随机密钥计算的 SipHash 标签，标签覆盖元数据与五元组哈希；读取方在魔数、版本、标签或长度任一不符时一律按不可信处理，元数据长度不得超过前部空间且不越过数据起点。用伪造标签、换五元组、截断、旧版本和空元数据验证。

## 线路格式（小端，紧邻数据起点向下增长）

| 偏移 | 字段 | 宽度 |
| --- | --- | --- |
| 0 | magic `0x444D534E` ("NSMD") | u32 |
| 4 | version（当前 `1`） | u16 |
| 6 | write_stage（写入阶段编号） | u16 |
| 8 | payload_len | u16 |
| 10 | total_len（含标签的整条记录长度） | u16 |
| 12 | payload，零填充至 4 字节对齐 | payload_len 向上取整 4 |
| 末尾 | SipHash-2-4 标签 | u64 |

标签用每次启动生成的 128 位随机密钥（`GenerateBootKey()`，Linux `getrandom` /
BSD·macOS `arc4random_buf`）计算，域分隔后覆盖：五元组的 SipHash
（`HashFiveTuple`，IPv4/IPv6 规范编码）与除标签外的整条元数据记录。因此记录
无法在无密钥情况下伪造，也不能从一条流搬到另一条流。

## 信任判定

`ReadMetadata()` 按序检查区域大小、magic、version、长度自洽性（`total_len`
不越过数据起点、与 `payload_len` 对齐后的期望长度一致）以及标签。任一项不符
都返回 `TrustStatus::kUntrusted*`，调用方必须按不可信处理，只有 `kOk` 才
可信任。标签比较为定长时间。写入侧 `WriteMetadata()` 在记录超过前部空间或
可能越过数据起点时直接拒绝写入。

## 用法

```cpp
#include "netsec.hpp"
using namespace netsec;

SipKey key = GenerateBootKey();              // 两端需共享同一启动密钥
FiveTuple tuple = /* 由报文解析得到 */;
uint8_t* meta = nullptr;
size_t meta_len = 0;
WriteMetadata(key, stage, payload, payload_len, tuple,
              data_start, headroom, &meta, &meta_len);

MetaView view;
TrustStatus st = ReadMetadata(key, meta, data_start, tuple, &view);
if (!IsTrusted(st)) { /* 丢弃信任判定 */ }
```

## 构建与测试

```sh
cmake -S . -B build-make -G "Unix Makefiles" \
  -DCMAKE_AR=/Library/Developer/CommandLineTools/usr/bin/ar \
  -DCMAKE_RANLIB=/Library/Developer/CommandLineTools/usr/bin/ranlib
cmake --build build-make
ctest --test-dir build-make --output-on-failure
```

`netsec_test` 覆盖：SipHash 参考向量、正常往返、空元数据、伪造标签/异密钥、
五元组替换（含 IPv6）、截断与长度字段篡改、旧版本与错误 magic、前部空间边界。
已通过 `-Wall -Wextra -Wpedantic -Wconversion -Wshadow` 与
AddressSanitizer/UndefinedBehaviorSanitizer。
