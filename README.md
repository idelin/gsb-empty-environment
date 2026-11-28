# BMFF / MP4 结构安全审计库

面向视频转码网关和内容安全服务的 C++17、零外部依赖、可嵌入 ISO BMFF /
MP4 原始字节审计库。审计发生在解码器之前，不依赖容器库或媒体框架。

## 能力

- 从文件偏移 `0` 严格解析顶层 Box，首盒必须是结构合法的 `ftyp`。
- 支持标准 32 位 `size`、`size == 1` 的 64 位 `largesize`，以及
  `size == 0` 的顶层末盒延伸语义。
- 严格拒绝 `size < 8`、64 位加法回绕、子盒越过父盒、容器内 `size == 0`、
  超出递归深度、重复或非顶层 `moov`。
- 递归审计 `moov`、`trak`、`mdia`、`minf`、`stbl`、`moof`、`traf`
  等容器，以及 `meta` 的 FullBox 前导。
- 精确解析 `stco`、`co64`、`stsc`、`stsz`，要求表项计数与盒体长度严格相等。
- 将 sample-to-chunk 映射展开为实际字节区间，验证每个 chunk 完整落在某个
  `mdat` 载荷内，绝不允许指向 `mdat` 头、`moov` 或其它元数据区。
- 检测跨轨/跨 chunk 重叠、`mdat` 未覆盖间隙、尾部附加数据、样本数、
  单帧尺寸及累计样本字节 DoS 炸弹。

## 构建

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

库目标为 `bmff_audit`，CLI 目标为 `bmff_audit_cli`。所有目标默认使用：

```text
-Wall -Wextra -Wconversion -Werror
```

## 嵌入

```cpp
#include "bmff/audit.hpp"

#include <cstdint>
#include <vector>

std::vector<std::uint8_t> bytes = load_uploaded_file();

bmff::AuditOptions options;
options.max_depth = 16;
options.require_mdat_coverage = true;

const bmff::AuditResult result = bmff::audit(bytes, options);
if (!result.ok()) {
    const bmff::AuditError& error = result.errors.front();
    // error.code、error.offset、error.message 可用于策略决策和审计日志。
    reject_upload(error.code, error.offset);
}
```

也可以直接审计内存指针：

```cpp
const bmff::AuditResult result =
    bmff::audit(data, size, bmff::AuditOptions{});
```

`AuditResult` 同时返回：

- `errors`：全部已发现错误，首项可通过 `first_error()` 获取。
- `boxes`：已审计 Box 的偏移、大小、头大小、深度和父 Box 索引。
- `mdats`：每个 `mdat` 的 Box 区间与载荷区间。
- `stats`：Box、`mdat`、chunk、sample、样本字节等统计。

## 默认限制

| 选项 | 默认值 |
| --- | --- |
| `max_depth` | 16 |
| `max_boxes` | 1,000,000 |
| `max_sample_count` | 10,000,000 |
| `max_chunk_count` | 10,000,000 |
| `max_single_sample_size` | 256 MiB |
| `max_total_sample_bytes` | 8 GiB |
| `require_mdat_coverage` | `true` |

阈值均可由宿主按业务档位覆盖。`require_mdat_coverage` 为 `true` 时，
每个 `mdat` 载荷都应由 sample chunk 无间隙、无重叠地完整覆盖；这也会拦截
追加在媒体数据之后的 Polyglot 尾载荷。若业务允许容器填充字节，可显式关闭，
但重叠与越界检查仍会执行。

## 样本表语义

- `stco` 与 `co64` 互斥；表项数必须精确匹配盒体长度。
- `stsz` 的 `sample_size != 0` 时只允许 12 字节固定头；为 `0` 时样本大小
  表必须精确包含 `sample_count` 项。
- 存在 `stsc` 时按 ISO BMFF 规则展开每个 chunk 的样本数，并要求覆盖
  `stsz` 的全部样本。
- 缺少 `stsc` 的简化输入仅在映射无歧义时接受：chunk 数等于 sample 数
  （一 chunk 一样本），或仅有一个 chunk（全部样本在该 chunk）。
- 区块偏移是文件绝对偏移，且必须满足
  `mdat.payload_offset <= offset`、`offset + chunk_size <= mdat.payload_end`。

## CLI

```sh
./build/bmff_audit_cli input.mp4
```

合法文件返回 `0`，结构拒绝返回 `1`，参数或 I/O 错误返回 `2`。

## 测试

测试不依赖外部 MP4 文件，全部由字节构造器手工生成，覆盖：

- 合法 `ftyp + moov/trak/mdia/minf/stbl + mdat` 样本，以及 `co64`、
  多 `stsc` 条目和跨 chunk 累加样本大小变体。
- 前置 Polyglot、非 ASCII `ftyp` 品牌、尾随垃圾、`size == 0` 顶层末盒。
- `largesize` 64 位加法回绕、`size < 8`、子盒越过父盒。
- `stco` 回指 `moov` 元数据区。
- 超过 16 层的嵌套容器。
- 重复 `moov`、`stsz`/`stco` 表长度不匹配、chunk 重叠、`mdat` 覆盖间隙。
- 超大单帧尺寸和跨轨总样本数 DoS 限制。

```sh
ctest --test-dir build --output-on-failure
```
