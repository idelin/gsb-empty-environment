# 字体表结构校验与规范化组件

面向 C++20 字体服务的可导入组件。它把 sfnt/TrueType 字体一次性解析成不可变的
`netsec::FontFace`，校验与渲染都只使用这一棵规范化解析树，渲染路径不会再次读取
表目录或原始偏移。

## 校验内容

- 逐表检查 `offset`、`length`、`checkSum`，表数据必须完全落在文件内。
- 所有表偏移必须四字节对齐，表数据（含四字节填充区）不得互相重叠，也不得覆盖
  sfnt 表目录。
- 可选校验 `head.checkSumAdjustment` 对应的整字体校验和。
- 校验 `head`、`maxp`、`hhea`/`hmtx`、`loca`、`glyf` 的基本结构与范围。
- 复合字形递归解析并检测自引用/互相引用环，组件字形 ID 必须落在 `numGlyphs` 和
  `loca` 范围内。
- `cmap` 各子表先做物理区间不重叠检查，再解析；支持格式 0、4、6、10、12、13，
  并验证格式 14 的变体映射。所有 cmap 字形 ID 与复合字形使用同一 `numGlyphs`
  上限。
- 多个 Unicode cmap 子表按确定的优先级归一化为一个规范化 cmap 区间集合。

## API

```cpp
#include "netsec.hpp"

netsec::ValidationOptions options;
options.verify_checksums = true;

netsec::FontLoadResult result =
    netsec::loadFont(bytes, options);  // bytes: std::span<const uint8_t>

if (result) {
    const netsec::FontFace& font = *result.font;
    auto glyph_id = font.glyphIndex(0x4E2D);
    netsec::RenderedGlyph rendered =
        font.renderCodepoint(0x4E2D);  // 与 glyphIndex() 使用同一棵树
    std::string fingerprint = rendered.fingerprint;
} else {
    for (const netsec::Diagnostic& diagnostic : result.diagnostics) {
        // diagnostic.code / table / offset / message
    }
}
```

`FontLoadResult::ok()` 表示结构校验通过。`FontFace::renderGlyph(glyph_id)` 与
`renderCodepoint(codepoint)` 返回相同字形时，`fingerprint` 与路径命令完全一致；
测试用这一点确认校验侧和渲染侧看到的是同一个字形。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试使用程序化构造的 TrueType 字体覆盖：合法字体、重叠表、越界偏移、未对齐、
校验和错误、cmap 越界字形引用、重叠 cmap 子表、复合字形环、长短 `loca`、格式
12 cmap，以及同一码点在规范化树上的渲染一致性。
