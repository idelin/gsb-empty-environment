为镜像解包器实现层合并组件：按层顺序应用白化文件、校验每层摘要与压缩格式一致、拒绝跨层路径逃逸与摘要不符的层。用被替换的中间层验证拒绝并给出层号与期望摘要。

## 实现

纯 Go 标准库实现，无外部依赖，代码位于 `layermerge/`，入口为
`layermerge.MergeLayers(rootDir, layers, opts)`。

- **按层顺序合并**：层按切片顺序逐个处理，后层覆盖/合并前层同路径内容。
- **白化文件（OCI whiteout）**：
  - `*.wh.<name>` 删除合并根中同目录下的既有条目；
  - `.wh..wh..opq` 标记不透明目录，先清空合并根中对应目录再写入新内容。
- **摘要校验**：对压缩后的原始 blob 计算 `sha256`/`sha384`/`sha512`，与
  `Layer.Digest` 比较；摘要在层内容提升到合并根之前校验，不符则整层拒绝。
- **压缩格式一致性**：通过魔数识别 gzip / bzip2 / zstd / 未压缩 tar，并与
  `Layer.MediaType`（OCI 与 Docker 常见类型）比对，不一致则拒绝。
- **路径逃逸防护**：
  - 在路径清洗前检查原始 tar 条目名，拒绝绝对路径、反斜杠与任何 `..` 组件
    （`archive/tar` 会静默清洗 `..`，必须提前拦截）；
  - 符号链接目标做词法包含检查，硬链接目标必须位于同层根目录内且存在；
  - 落地时逐级校验父目录不是符号链接，防止沿 symlink 写出根目录。
- **失败原子性**：每层先在独立暂存目录完成解压、校验、解包与白化，全部通过
  后才提升（rename，跨设备回退复制）到合并根。任一层失败时，该层及其后的层
  都不会改动合并根，根目录保持上一个成功层的状态。

拒绝错误统一为 `*layermerge.LayerError`，包含 1 起的层号 `Index`、失败类别
`Kind`、相关路径以及摘要不符时的 `Expected`/`Got` 摘要。

## 验证

```sh
go test ./...
go run ./cmd/layermerge-demo
```

`TestRejectReplacedMiddleLayerDigestMismatch` 构造 3 个层并替换中间层 blob、
保留旧摘要，断言返回 `LayerError{Index: 2, Kind: ErrDigestMismatch}`，错误消息
带层号与期望摘要，且失败层与后续层均不落地。`cmd/layermerge-demo` 以 4 层
场景（被替换的是第 3 层）端到端演示同样的拒绝信息，并给出摘要匹配的对照组。
