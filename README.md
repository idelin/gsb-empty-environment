# netsec

供 C++20 归档服务导入的安全解包组件。它把归档条目视为不可信输入，默认拒绝硬链接和符号链接；只有显式启用 `LinkPolicy::AllowWithinArchive` 才允许在目标根目录和归档成员集合内处置链接。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

库目标为 `netsec`，公开头文件为 `netsec.hpp`。

## API

```cpp
#include "netsec.hpp"

std::vector<netsec::ArchiveEntry> entries;

netsec::ArchiveEntry file;
file.path = "payload/data.txt";
file.type = netsec::EntryType::RegularFile;
file.data = "contents";
file.permissions = std::filesystem::perms::owner_read |
                   std::filesystem::perms::owner_write |
                   std::filesystem::perms::group_read;
entries.push_back(file);

netsec::ArchiveEntry hard;
hard.path = "payload/data-hardlink.txt";
hard.type = netsec::EntryType::HardLink;
hard.link_target = "payload/data.txt"; // 已先解包的归档成员
entries.push_back(hard);

netsec::ExtractOptions options;
options.hard_links = netsec::LinkPolicy::AllowWithinArchive;

auto result = netsec::extract_archive("/srv/import/root", entries, options);
if (!result) {
    // result.error.code / result.error.message / result.error.path
}
```

`NormalizedPath normalize_archive_path(std::string_view)` 可供调用方在进入提取器前验证成员路径。

## 安全约束

- 成员路径必须相对、以 `/` 分隔，拒绝绝对路径、空组件、`..`、反斜杠和 NUL。
- 链接目标必须是规范化后的归档成员；硬链接目标必须是更早出现且已解包的普通文件，符号链接目标必须是更早出现的普通文件或目录。
- 符号链接内容可为相对路径，但 `..` 规范化后必须仍落在归档根内；落盘文本会改写为不含中间 `..` 的等价相对路径，避免实际解析经过既有链接。
- 任何条目都不能穿过文件、硬链接或符号链接父节点；已有目标目录中的符号链接父目录同样会被 `O_NOFOLLOW` 拒绝。
- 普通文件、硬链接和符号链接均先在同一父目录创建唯一临时对象，再通过 `renameat` 原子替换目标。普通文件在 `fsync` 后才替换。
- 不跟随目标叶节点上的既有符号链接或硬链接；原子替换只替换目录项，不会修改链接所指向的外部文件。
- `setuid`、`setgid`、sticky 位总是拒绝。默认还拒绝 group/other write 位；可用 `allow_group_or_world_writable` 放宽。
- 显式权限会保存；目录在提取期间暂时保留属主读写/搜索权限，全部条目完成后再按深度逆序应用最终权限。未提供权限时使用安全默认值（文件 `0644`、目录 `0755`）。硬链接若声明权限，必须与源文件一致。

提取器不做整体事务回滚：中途失败时，之前已成功安装的条目会保留。失败路径不会安装当前条目，也不会遗留临时文件。

## 测试覆盖

`tests.cpp` 覆盖硬链接绝对路径和父目录逃逸、链接目标不在归档、链接先于目标、符号链接默认拒绝及归档内允许、既有符号链接父目录重定向、叶节点链接替换、权限位拒绝、合法目录/文件/硬链接/符号链接归档，以及原子替换失败保留旧文件。
