# polyglot

上传服务按扩展名和首个魔数判定类型，攻击者用同时是GIF与JavaScript、PDF与ZIP的多义文件绕过内容策略。给Go上传处理器实现类型判定包，调用方提供ReaderAt和大小：按各格式自身规则定位结构，ZIP从尾部找中央目录，PDF允许前置垃圾字节，GIF与PNG校验块链完整；同一文件能被两种格式成功解析时拒绝。构造合法单格式文件与多义样本，核对判定结果和拒绝原因。

## 设计

包 `polyglot` 不依赖扩展名或首个魔数，而是按每种格式自身的结构规则定位并校验：

- **PNG**（`png.go`）：校验 8 字节签名与块链，要求 IHDR 为首块、IEND 为末块，IEND 之后存在任何尾随字节即判无效。
- **GIF**（`gif.go`）：校验 GIF87a/GIF89a 版本头、逻辑屏幕描述符、全局/局部颜色表、图像与扩展块链，直到终止符 `0x3B`；终止符后的尾随数据（例如追加的 JavaScript）直接拒绝。
- **PDF**（`pdf.go`）：在文件前 1024 字节内定位 `%PDF-` 头（允许前置垃圾字节），在尾部 64KiB 窗口内定位最后一个 `%%EOF` 与前置 `startxref`，再按偏移核对交叉引用表（`xref`+`trailer`）或交叉引用流（`/XRef`）。
- **ZIP**（`zip.go`）：从尾部 64KiB 窗口反向扫描 End Of Central Directory，核对注释长度与文件末尾一致，再沿中央目录逐个校验文件头与本地文件头签名；支持 ZIP64 EOCD 记录、定位器与 extra 字段。

所有读取经 `boundedReader`（`reader.go`）做严格边界检查，越界偏移返回明确错误而非静默截断。

## 判定规则

入口 `Detect(io.ReaderAt, size)`：

- 恰好一种格式解析成功 → 返回该格式；
- 两种及以上格式解析成功 → 返回 `*AmbiguousError`（`errors.Is` 命中 `ErrAmbiguous`），拒绝原因列出全部命中格式及结构定位说明；
- 没有格式能解析 → 返回 `*UnknownError`（`errors.Is` 命中 `ErrUnknown`），逐格式给出失败原因。

另有 `DetectWithExtension` 核对声明扩展名、`MustReject` 直接给出是否拒绝与审计原因。

## 测试

测试在 `*_test.go` 中构造真实合法样本（PNG/GIF/PDF/ZIP，含 ZIP64 与带前置垃圾的 PDF）与多义样本（PDF+ZIP、GIF+JavaScript），并用标准库 `archive/zip` 交叉验证 ZIP 类夹具确实可被独立解析：

```sh
go test -race -cover ./...
```

关键断言：PDF/ZIP 多义文件命中 `ErrAmbiguous` 且原因同时包含 `.pdf` 与 `.zip`；GIF 后追加 JavaScript 因终止符尾随数据命中 `ErrUnknown`。
