# gif_audit — 原始字节级 GIF87a/GIF89a 安全审计库

面向内容安全网关的可嵌入 C++17 库，在把用户上传的 GIF 交给渲染器或缩略图
服务之前，对**完整容器结构与 LZW 压缩码流**做严格的原始字节级校验。仅核对
6 字节魔数无法防御本库覆盖的攻击面；本库不分配任何像素缓冲，内存占用恒定为
一个 4096 项的 LZW 字典。

零外部依赖（仅标准库），单头文件，可使用
`-Wall -Wextra -Wconversion -Werror -pedantic` 干净编译。

## 集成

```cpp
#include "gif_audit/gif_audit.hpp"

gif_audit::AuditResult r = gif_audit::audit(bytes.data(), bytes.size());
if (!r) {                       // 等价于 !r.ok
    log(r.code, r.offset, r.detail);   // 机器可读错误码 + 字节偏移
    reject_upload();
}
```

也接受 `std::vector<std::uint8_t>` 重载；`gif_audit::Limits` 可自定义画布与
像素配额（默认值见头文件）。

## 审计保证

### 1. 头部 / 逻辑屏幕描述符 / 调色板
- 魔数必须精确为 `GIF87a` 或 `GIF89a`，否则 `BadSignature`。
- 画布宽高非零并满足 `Limits` 配额；`width*height` 受总像素配额约束。
- 全局/局部颜色表按 `2^(N+1)` 项 ×3 字节精确核对，越界即
  `TruncatedGlobalColorTable` / `TruncatedLocalColorTable`。
- 背景色索引越界、无 GCT 却设置背景索引均被拒绝。

### 2. 逐块容器解析
- 顺序解析图像描述符 `0x2C`、扩展块 `0x21`（`0xF9` 图形控制、`0xFE` 注释、
  `0xFF` 应用、`0x01` 纯文本）与唯一结束符 `0x3B`；未知引入子/扩展标签硬拒绝。
- 每个子块长度与负载都对输入末尾做边界检查：
  子块越界 → `SubBlockOverrun`；缺失 `0x00` 终结符 →
  `TruncatedBlock` / `MissingBlockTerminator`。
- **`0x3B` 之后出现任何字节都被拒绝**（`TrailingBytesAfterTrailer`），拦截
  GIFAR / HTML-JS Polyglot 尾部藏毒；缺失 Trailer 则 `MissingTrailer`。
- 任意帧 `left+width > screenWidth` 或 `top+height > screenHeight`
  → `ImageFrameOutOfBounds`（32 位提升，加法不可能回绕）。
- GCE 块大小/保留位/处置方法、图像描述符保留位等畸形字段均被拒绝。

### 3. LZW 码流完整模拟（LSB-first，位宽 minCodeSize+1 → 12）
- `LZW Minimum Code Size` 必须落在 `[2, 8]`，否则
  `InvalidLzwMinCodeSize`（杜绝超大移位/栈缓冲尺寸问题）。
- 字典固定分配 4096 项；引用未定义的未来码字（含首码即 KwKw）→
  `LzwFutureCode`。
- 不发 `Clear Code` 却试图创建第 4097 项 → `LzwTableFullNoClear`
  （杜绝朴素解码器的堆越界写；清表逻辑本身有界，延迟清表无法溢出）。
- 输出像素数超过帧 `width*height` 或配额 → `LzwOutputOverflow`
  （解压炸弹）；EOI 时像素不足 → `LzwOutputShort`；位流提前耗尽 →
  `LzwTruncated`。
- 位读取器逐位取数，码宽恒 ≤12 位、位移量恒 <32，无未定义行为。

## 错误码

所有拒绝都映射到稳定的 `gif_audit::ErrorCode` 枚举，可用
`gif_audit::error_code_name()` 转为字符串，便于网关输出结构化遥测。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试程序 `tests/gif_audit_test.cpp` 内置一个最小、符合规范的 LZW 编码器，
在原始字节层构造全部样本，覆盖：

- 合法 GIF89a（含 GCE/注释/应用扩展）与 GIF87a；
- `minCodeSize=12` 与 `minCodeSize=1`；
- 非法未来 LZW 码字；
- 解压炸弹（输出像素 > 帧尺寸）与字典超 4096 不发 Clear；
- 帧越界画布、缺失/损坏块终结符、子块长度越界；
- `0x3B` 后尾随 Polyglot 载荷、缺失 Trailer；
- 本地调色板、`minCodeSize=8`、配额裁剪等边界。

测试在 `-fsanitize=address,undefined` 下同样零报告。

## 文件

- `include/gif_audit/gif_audit.hpp` — 单头文件审计库（接口 + 实现）。
- `tests/gif_audit_test.cpp` — 34 项自包含单元测试。
- `CMakeLists.txt` — INTERFACE 库目标 `gif_audit` 与测试目标。
