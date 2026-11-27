应用商店与制品网关在校验 ZIP/JAR/APK 归档包时，若仅调用高层解压库遍历中心目录（CDH），攻击者可构造本地文件头（LFH）与中心目录声明不一致的多义性压缩包（如 Janus/MasterKey 式签名绕过）、同名重复条目使验签器读第一个而运行时读最后一个、利用相对路径/符号链接实施 Zip Slip 任意写，或构造重叠偏移与超高压缩比的 Zip Bomb 耗尽磁盘与内存。为 Go 制品安全宿主实现可导入的原始字节级 ZIP/JAR 结构一致性与安全准入验证包，严禁依赖 `archive/zip` 高层遍历替代底层头校验，严禁联网。组件直接扫描原始字节流定位 EOCD（拦截尾随恶意数据或多重伪造 EOCD）、解析全部 CDH 与对应的 LFH 及 Data Descriptor：（1）硬拒绝任意 LFH 与 CDH 在文件名、压缩方式、CRC-32、压缩/解压大小或通用位标志上的不一致，拒绝文件数据区间相互重叠或存在未被引用的隐藏间隙载荷；（2）硬拒绝归档内出现规范化后重名条目（含大小写折叠冲突与 `\`/`/` 混合分隔符冲突）、绝对路径、`..` 穿越、以及 Unix 外部属性带符号链接（`S_IFLNK`）或 `SUID`/`SGID` 的危险条目；（3）执行流式解压配额监控（基于实际解压字节计数而非信任头声明的 `uncompressed_size`），检出单文件解压超限、总解压字节超限与压缩比超阈值炸弹。附手工构造的 LFH/CDH 差异样本、重名覆盖、重叠偏移炸弹与 Zip Slip 样本测试。

## `zipguard` 包

导入路径：`formal.local/w28-539424a87d/zipguard`

纯标准库实现（`compress/flate`、`hash/crc32`、`encoding/binary` 等），**不导入
`archive/zip`**，所有结构均直接从原始字节解析，无网络依赖。

### 入口 API

- `VerifyBytes(b []byte, policy Policy) (*Report, error)`：校验完整内存归档。
- `VerifyReader(r io.Reader, policy Policy) (*Report, error)`：按 `MaxArchiveSize`
  限流读入后校验。
- `DefaultPolicy() Policy`：制品网关默认配额（条目数、归档大小、单文件/总量解压
  配额、压缩比阈值、符号链接/SUID/SGID/特殊文件开关）。
- 错误用 `errors.Is` 分类，哨兵错误定义在 `zipguard/errors.go`。

### 校验流水线

1. **EOCD 定位**：在原始尾部窗口内反向扫描 EOCD，硬拒绝非终止位置的伪造 EOCD
   签名（含注释内嵌、载荷内嵌、多重 EOCD）、EOCD 之后的尾随数据、分卷归档；
   支持 ZIP64 EOCD/locator。
2. **CDH 全量解析**：按 EOCD 声明的偏移与长度逐条解析中心目录，校验签名、条目
   数、磁盘号、压缩方式（仅允许 Stored/Deflate）与 ZIP64 占位字段。
3. **LFH 与 Data Descriptor 校验**：逐条解析 LFH，硬性比对文件名、压缩方式、
   通用位标志、CRC-32、压缩/解压大小（bit 3 数据描述符场景下 LFH 允许零字段，
   但描述符必须与 CDH 一致）。
4. **全字节区间覆盖**：把每个 LFH+载荷(+描述符)、每条 CDH、EOCD/ZIP64 记录映射
   为半开区间，排序后要求从首字节到末字节无缝衔接——任何重叠（`ErrOverlap`）或
   未引用间隙（`ErrGap`）一律拒绝。
5. **名称与模式准入**：反斜杠归一为 `/` 并做 ASCII 大小写折叠后查重；拒绝绝对
   路径、盘符、UNC 路径、任意 `..` 段；按传统 `mode_t` 位识别 `S_IFLNK`、
   `SUID`/`SGID` 及设备/FIFO/Socket 特殊文件。
6. **流式解压配额**：Deflate 以流式 `io.Copy` 接入带配额 writer，按**实际吐出的
   字节**计数（不信任头部 `uncompressed_size`），中途触发单文件/总量上限即中止；
   完成后核对实际 CRC-32、实际大小与声明值，并对足够大的载荷计算实际压缩比。

### 测试

`go test ./...`（另可用 `-race`、`-cover`）。测试内置手工 ZIP 构造器，覆盖：

- LFH/CDH 在 CRC、大小、方式、标志、名称上的六类差异样本；
- CDH 谎报解压大小（实际字节计数戳穿）、载荷 CRC 损坏、Data Descriptor 不一致；
- 同名/大小写/分隔符冲突重名条目（MasterKey 式读取歧义）；
- Zip Slip（`..`、绝对路径、盘符、UNC、混合分隔符）与符号链接/SUID/SGID/特殊文件；
- 手工构造的重叠数据区间、隐藏间隙载荷、Janus 式前缀、伪造/多重 EOCD、尾随数据；
- 2 MiB 零值高压缩比炸弹、单文件与总量解压配额中途中止、截断 Deflate 流；
- ZIP64 归档（占位符 + extra 字段 + ZIP64 EOCD）正向与损坏用例。
