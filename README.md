## mediasafe：原始字节级结构安全审计库

### 设计目标

- 可嵌入：仅暴露 `include/mediasafe/common.h`，编译为静态库，无第三方依赖。
- 内存安全：所有多字节读取经边界检查的 `ByteReader` 逐字节拼装，无对齐 UB、无裸指针算术越界；恶意输入只返回 `AuditResult{ok=false, error}`，不抛异常。
- 整数安全：像素预算、`count * type_size`、IFD 块大小全部走可移植的 `add_overflow_u64` / `mul_overflow_u64`（不依赖编译器内建函数）。

### 目录结构

- `include/mediasafe/common.h`：公共 API、`AuditLimits`、`AuditResult`、溢出工具。
- `src/crc32.cpp`：内置 IEEE 802.3 CRC-32（反射多项式 `0xEDB88320`）。
- `src/reader.h`：端序感知、边界检查的字节读取器。
- `src/png_audit.cpp`：PNG 逐块审计。
- `src/tiff_audit.cpp`：经典 TIFF（II/MM）与 BigTIFF 审计。
- `src/dispatch.cpp`：按签名自动分派。
- `tests/`：手工构造样本的测试（合法样本 + 各类攻击样本）。

### 公共 API

```cpp
mediasafe::AuditLimits limits;          // 宿主资源上限（像素/文件/IFD 深度）
auto r1 = mediasafe::audit_png(p, n, limits);
auto r2 = mediasafe::audit_tiff(p, n, limits);
auto r3 = mediasafe::audit(p, n, limits);   // 自动嗅探 PNG/TIFF 签名
```

### PNG 审计项

- 8 字节魔数精确匹配；逐块校验长度高位为 0（`<= 2^31-1`）与真实 IEEE CRC-32。
- 首块必须是唯一 `IHDR`，末块必须是 `IEND`，且 `IEND` 后禁止任何尾随字节。
- `IHDR` 宽高非零且 `<= 2^31-1`，位深/色彩类型组合合法，压缩/滤波/隔行参数受限。
- 64 位无溢出计算 `ceil(width*channels*bit_depth/8) * height`，超过 `max_pixel_bytes` 即拒绝。
- `PLTE` 长度须为 3 的倍数且不超过 256 项；重复关键块、非连续 `IDAT` 均拒绝。

### TIFF 审计项

- 识别 `II`/`MM` 端序，支持经典 32 位（magic 42）与 BigTIFF（magic 43，16 字节头）。
- 维护已访问 IFD 偏移集合与递归深度上限，硬拦截主链/`SubIFD`/`ExifIFD`/`GPSIFD` 的任意环路与超深嵌套（防无限循环 DoS）。
- 每个 Tag 校验类型合法、`count * type_size` 无溢出；外联载荷指针与范围必须完整落在文件内（防越界读）。遵循 TIFF6 内联值左对齐规则，端序无关。
- `StripOffsets` 与 `StripByteCounts` 必须同时存在且数量配对；逐条校验范围并检测条带重叠（Strip Overlap）；像素总字节按无溢出算术比对宿主上限。

### 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

库与测试均以 `-Wall -Wextra -Wconversion -Werror -pedantic` 编译，并额外通过 `-Wshadow` 核验。在某些 macOS 环境下 `/usr/bin/ar` 是失效的 xcode-select 垫片，需向 CMake 显式传入真实归档器：

```sh
cmake -S . -B build \
  -DCMAKE_AR=/Library/Developer/CommandLineTools/usr/bin/ar \
  -DCMAKE_RANLIB=/Library/Developer/CommandLineTools/usr/bin/ranlib
```

---

内容安全网关与缩略图预处理服务在将用户上传的 PNG 与 TIFF 图像交给底层图形渲染库之前，若未做原始字节级结构审计，攻击者可构造超大 `IHDR` 宽高乘积触发 32/64 位整数溢出堆越界写、利用 CRC 伪造或重复 `IHDR`/截断 `IDAT` 触发解析器未定义行为，或在 TIFF 中构造 `IFD` 链表环路（无限循环 DoS）、越界偏移/条带重叠（Strip Overlap）以及指向文件头外的嵌套 `ExifIFD`/`SubIFD` 指针导致越界读。为 C++17 媒体安全宿主实现可嵌入、零外部依赖、`-Wall -Wextra -Wconversion -Werror` 编译通过的 PNG 与 TIFF 原始字节级结构安全审计库（内置 IEEE CRC-32）。库严格执行两类二进制格式审计：（1）PNG 审计：校验 8 字节魔数、逐块校验长度（最高位须为 0，≤2^31-1）与真实 IEEE CRC-32、强制首块为唯一 `IHDR` 且末块为 `IEND`（拦截 `IEND` 后尾随藏毒），校验 `IHDR` 宽高/位深/色彩类型合法组合并按无溢出 64 位算术核验解压后像素缓冲不超过宿主上限，拒绝非法 `PLTE` 长度（非 3 的倍数或超 256 项）及重复关键块；（2）TIFF（含大端 `MM` 与小端 `II` 经典 32 位及 BigTIFF 校验）审计：追踪全部主 `IFD` 与递归子 `SubIFD`/`ExifIFD` 偏移，用已访问偏移集合与深度上限硬拦截任意 IFD 环路与超深嵌套，校验所有 Tag 偏移与 `count * type_size` 无溢出且严格落入文件范围内，校验 `StripOffsets`/`StripByteCounts` 长度配对一致且像素总字节不超上限。附手工构造的合法 PNG/TIFF 样本与宽高整数溢出、CRC 篡改、IEND 尾随载荷、TIFF IFD 环路及越界子 IFD 样本测试。
