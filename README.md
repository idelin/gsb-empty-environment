# ext4_audit

离线、只读、零外部依赖的 C++17 ext4 镜像原始字节级安全审计库，用于容器镜像与
虚拟机磁盘在**挂载不可信 ext4 镜像之前**的准入检查。所有解析均基于镜像内存字节，
不执行任何挂载、系统调用或镜像内代码。

## 检测能力

库从字节偏移 1024 的 `ext4_super_block` 开始严格解析：

1. **Superblock / 块组校验**
   - 魔数 `0xEF53`、块大小仅接受 1K/2K/4K（`s_log_block_size` 0/1/2）。
   - 总块数 × 块大小与镜像实际长度一致，全部乘法使用 64 位溢出检查。
   - 解析块组描述符表，同时支持 32 位与 64 位（`INCOMPAT_64BIT`）描述符，
     校验每个 Inode 表区间完全落在镜像内。
2. **Inode 与目录项遍历**
   - 跳过 mode 为 0 的未使用槽位，逐个审计已分配 Inode。
   - 线性目录项严格校验 `rec_len`（>=8、4 字节对齐、不跨块、容纳名字），
     并以单块最大条目数兜底，防止死循环与越界读取。
3. **Extent 树递归校验**（`EXT4_EXTENTS_FL`）
   - 节点魔数 `0xF30A`；子节点深度必须严格递减 1。
   - 逻辑块号单调递增、相邻区间无重叠；深度上限防止病态递归。
   - 已访问节点集合检测环路（含索引节点自引用）。
   - 每个物理块均校验落入 `[1, total_blocks)` 合法区间，识别未初始化长度位。
4. **交叉链接与策略违例**
   - 全局物理块归属表精确检出两个不同 Inode 共享同一数据块，
     违例携带共享块号见证与对端 Inode/路径。
   - 检出全部非白名单 `SUID`/`SGID` 位。
   - 检出任意 `S_IFBLK`/`S_IFCHR` 设备节点（支持新旧设备号编码），
     默认全部拒绝，可按 `(major, minor)` 白名单放行。
   - 输出违例类型、Inode 号、重建路径与块见证。

## 集成方式

库仅暴露一个公共头，可直接加入宿主工程：

```cpp
#include "ext4_audit/ext4_audit.h"

ext4_audit::AuditReport report =
    ext4_audit::audit_file("/path/to/image.img");
if (!report.ok()) {
  for (const auto& v : report.violations) {
    // v.type / v.inode / v.path / v.witness_blocks / v.detail
  }
}
```

也可对内存中的镜像字节调用 `audit_image(data, size, config)`。白名单通过
`AuditConfig` 的 `suid_inode_whitelist`、`sgid_inode_whitelist` 与
`device_major_minor_whitelist` 配置。

## 构建与测试

要求 C++17，无第三方依赖。

```sh
make test     # 严格标志编译并运行 23 项断言
make cli      # 构建 examples/audit_cli 命令行工具
```

或使用 CMake：

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

所有目标均以 `-Wall -Wextra -Wconversion -Werror` 编译，并额外通过
`-Wpedantic -Wshadow` 检查。

## 测试镜像

`tests/image_builder.h` 以纯字节方式手工拼装一个 64 块 / 16 Inode 的合法微型
ext4 镜像（无需 root、无需 mount），并在此基础上构造攻击变体：

- 损坏魔数、截断镜像、非法块大小、块数与长度不一致。
- Extent 树自引用环路、深度不递减、逻辑块重叠、物理块越界。
- 两个 Inode 交叉引用同一数据块。
- 隐藏 `S_IFBLK` 设备节点（major 8，指向宿主磁盘）与非白名单 SUID/SGID。
- 越界 / 非对齐的目录项长度。
- 64 位块组描述符解析路径与白名单放行路径。

## 安全边界

- 审计目标是结构与准入策略，不校验 ext4 校验和、不解释日志，也不恢复文件内容。
- 旧式非 extent 块映射（direct/indirect）不被静默信任：检测到非空旧式映射时
  报告 `LegacyMappingUnsupported`，要求宿主拒绝该镜像，而不是绕过校验。
- 符号链接的快速内联目标按 ext4 语义识别，不触发误报。
