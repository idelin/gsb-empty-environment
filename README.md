# safeunpack

`safeunpack` 是一个仅依赖 Go 标准库的归档准入验证与“虚拟解包”包。它不会写入宿主文件系统，而是按原始字节顺序解析归档，并把结果保存在精确的内存目录树中。适用于 CI/CD 构建宿主、制品扫描网关和上传前校验。

支持：

- USTAR / PAX TAR（`x` 局部扩展头、`g` 全局扩展头、`path`/`linkpath`/`size` 覆盖）
- gzip 压缩的 TAR（自动探测，流式压缩比计量）
- ZIP（Store/Deflate、EOCD、ZIP64、中心目录/本地头/数据描述符一致性）

## 快速使用

```go
data, _ := os.ReadFile("artifact.tar")

limits := safeunpack.DefaultLimits()
limits.MaxEntries = 5_000
limits.MaxFileBytes = 32 << 20
limits.MaxTotalBytes = 128 << 20
limits.MaxCompressionRatio = 100

result, err := safeunpack.UnpackArchiveBytes(
	data,
	safeunpack.WithLimits(limits),
)
if err != nil {
	// errors.Is(err, safeunpack.ErrPathTraversal) 等可用于分类拒绝原因。
	return err
}

file, ok := result.Lookup("dist/app")
if ok {
	// file.Data 是受配额约束的内存内容；不会触达真实文件系统。
	_ = file.Data
}
```

主要入口：

- `UnpackTAR` / `UnpackTARBytes`：解析 USTAR/PAX，兼容 gzip 输入
- `UnpackTARGzip`：强制按 gzip TAR 解析
- `UnpackZIP` / `UnpackZIPBytes`
- `UnpackArchive` / `UnpackArchiveBytes`：按魔数自动选择格式
- `ValidateTAR`、`ValidateZIP`、`ValidateArchive` 及其 `*Bytes` 形式：只返回错误
- `NewScanner(opts...)`：复用一组限额，提供 `Scanner.Validate*` 和 `Scanner.Unpack*`

所有扫描都会新建独立的虚拟树，`Scanner` 本身不保存跨归档状态。

## 限额

`Limits` 的零值使用默认值，负值可关闭对应字节数或比例限制。

| 字段 | 默认值 | 作用 |
| --- | --- | --- |
| `MaxEntries` | 10,000 | 最大物理归档记录数（TAR 的 PAX 头也计数） |
| `MaxFileBytes` | 64 MiB | 单个成员解码后上限 |
| `MaxTotalBytes` | 256 MiB | 累计解码成员数据上限 |
| `MaxArchiveBytes` | 512 MiB | ZIP 随机访问镜像读取上限 |
| `MaxCompressionRatio` | 200 | 解码/压缩字节比阈值 |
| `MaxPathBytes` | 4,096 | 规范化路径或链接目标字节上限 |

## 安全边界

- 拒绝绝对路径、Windows 盘符/反斜杠路径、NUL、越出根目录的 `..` 条目。
- 允许根内的相对符号链接和硬链接。符号链接目标会进行根内解析并检测环路。
- 实时记录已创建的符号链接。后续普通文件、目录、符号链接或硬链接只要任一父目录命中已创建的符号链接，整包立即失败。
- 硬链接目标必须已存在、不是目录、不是符号链接，且自身路径不得穿过符号链接。
- TAR 校验 USTAR 校验和（兼容零填充与空格填充两种历史算法），拒绝畸形八进制、负尺寸、伪造校验和及非零数据填充。
- PAX 扩展头按记录声明长度严格切分；局部头只作用于下一条记录，全局头持续继承，局部值覆盖全局值。
- ZIP 校验中心目录与本地头中的名称、标志、方法、CRC、大小及可选数据描述符；拒绝重叠本地记录、加密、跨盘和不支持的压缩方法。
- 禁止静默覆盖。所有已物化路径及隐式父目录都参与大小写归一化冲突检测（同一路径也不能重复出现）。
- TAR gzip 与 ZIP Deflate 按块计量输出，并在读取前先执行单文件、累计字节和压缩比检查。

虚拟树中的普通文件内容保存在 `Node.Data`；符号链接保留原始相对目标 `LinkTarget` 与已验证的根内目标 `ResolvedTarget`；硬链接通过 `ResolvedTarget` 指向已存在的普通文件。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖字节级 PAX 路径覆盖、两段式符号链接父目录穿越、硬链接越权/悬空/指向符号链接、伪造校验和、畸形八进制、大小写冲突、TAR gzip 与 ZIP 高压缩比炸弹，以及合法的 TAR/PAX、tar.gz 和 ZIP（含 ZIP64）样本。

