# WebP/RIFF 原始字节级安全审计库

在将用户上传的 WebP 送入底层解码库**之前**执行的纯字节级容器/帧头审计。
单头文件、零外部依赖、不抛出异常、不做任何像素解码，仅依据 RIFF 结构与
帧头字段判定输入是否可安全放行。

## 集成方式

拷贝 `include/webp_audit.hpp` 即可（header-only）：

```cpp
#include "webp_audit.hpp"

webp_audit::AuditOptions opt;          // 默认 max_dimension=16384
opt.max_pixels = 256ull * 256ull * 4;  // 自定义宿主像素配额
auto r = webp_audit::audit(bytes.data(), bytes.size(), opt);
if (!r.ok()) {
    reject(webp_audit::to_string(r.error), r.offset);  // offset 指向问题字节
}
use(r.info.width, r.info.height, r.info.frame_count);
```

同时提供 `std::string_view` 重载。所有长度运算使用 64 位提升，杜绝 32 位回绕。

## 审计内容

**1. RIFF 12 字节头**
- 魔数 `RIFF` 与 `WEBP` 标识精确匹配。
- `fileSize` 以 64 位运算校验 `fileSize + 8 == input.size()`：任何 32 位回绕、
  截断或容器末尾夹带的未声明载荷都会被 `kBadRiffSize` 拒绝。

**2. 逐块顺序解析**
- 每个块校验 8 字节头（FourCC + 小端 `chunkSize`），`payload` 越界即
  `kChunkPayloadOutOfBounds`，块头截断即 `kTruncatedChunkHeader`。
- 奇数 `chunkSize` 后必须恰好跟随 1 字节 `0x00` 填充：缺失返回
  `kMissingPaddingByte`，非零藏毒返回 `kNonZeroPadding`（防 Chunk Smuggling）。
- 简单格式：首块必须是 `VP8 `/`VP8L`，且**只允许**这一个块。
- 扩展格式：首块必须是恰好 10 字节的 `VP8X`；保留标志位/保留字节非零即拒；
  `ICCP`/`ALPH`/`EXIF`/`XMP `/`ANIM` 的实际出现情况与 VP8X 标志位严格双向一致
  （`kFlagChunkMismatch`）；按规范强制块顺序并拒绝重复块、重复/冲突帧块；
  未知 FourCC 直接 `kUnknownChunk`。

**3. 帧头与画布配额**
- `VP8 `：要求关键帧（帧标签 bit0=0）、起始码 `9d 01 2a`，解析 14 位宽高。
- `VP8L`：校验 `0x2f` 签名，按位打包还原 14 位宽高与 alpha hint。
- `ANMF`：解析 24 位 `x/y` 与 1+24 位宽高，保留标志位非零即拒；以减法形式做
  溢出安全的 `x + width <= canvasWidth`、`y + height <= canvasHeight` 判定；
  递归审计其内部 `ALPH`/`VP8 `/`VP8L` 子块（含填充），并要求内部位流尺寸与
  ANMF 帧头尺寸完全一致，否则 `kFrameDimensionMismatch`。
- 画布与每帧都校验零尺寸、单边上限 `max_dimension` 与像素总数 `max_pixels`。

失败时 `AuditResult::offset` 给出触发拒绝的字节偏移，便于日志定位。

## 构建与测试

要求 C++17，按题目约束以 `-Wall -Wextra -Wconversion -Werror` 编译。

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

或直接用编译器：

```sh
clang++ -std=c++17 -Wall -Wextra -Wconversion -Werror -Iinclude \
    tests/test_webp_audit.cpp -o test_webp_audit && ./test_webp_audit
```

## 测试覆盖

`tests/test_webp_audit.cpp` 在内存中构造样本（无外部文件依赖）：
- 合法用例：简单 `VP8 `、带奇数零填充的简单 `VP8L`、含 ICCP/ALPH/EXIF/XMP 的
  扩展静态图、多帧 `ANMF` 动图，并断言解析出的画布/帧数/特性标志。
- 攻击/畸形用例：非零奇偶填充、缺失填充、RIFF 长度 32 位回绕、尾随隐藏数据、
  VP8X 标志与子块双向矛盾、ANMF 越界画布、ANMF 与内部位流尺寸矛盾、块长度越界、
  块头截断、重复/乱序块、坏 VP8 起始码/非关键帧、坏 VP8L 签名、像素配额超限、
  未知块、VP8X 保留位与错误长度、空/空指针输入。

`tests/fuzz_webp_audit.cpp` 注入 2 万组随机与伪 RIFF 字节，配合
`-fsanitize=address,undefined` 验证任意畸形输入下都不存在越界读或未定义行为。

## 文件

- `include/webp_audit.hpp`：header-only 审计库（全部实现）。
- `tests/test_webp_audit.cpp`：合法样本与定向攻击用例。
- `tests/fuzz_webp_audit.cpp`：内存安全模糊测试。
