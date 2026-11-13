# kustomization引用与生成器准入

`netsec` 是纯标准库实现的 kustomization 准入包，供 Go 构建网关在执行
kustomize 构建前调用。包本身不访问网络、不读取真实文件系统：调用方负责
解析 YAML、枚举仓库文件并传入远程主机白名单。

## 准入规则

- 本地路径（`resources`、`bases`、`patches.path`、生成器/转换器插件路径、
  `configMapGenerator`/`secretGenerator` 的 `env` 与 `files`）：词法规范化后
  必须位于仓库根内，按目录逐级解析符号链接后的最终目标同样必须在根内；
  符号链接环与超过 64 跳的链直接拒绝。
- 远程引用（`resources`、`bases`）：只允许 `https`，主机必须在白名单内，
  禁止 userinfo；`ref` 必须是恰好 40 位十六进制提交号，分支名、标签名、
  短哈希以及缺省 ref 一律拒绝。`::` 形式、scp 形式与无 scheme 的
  `host/path?ref=` 形式同样拒绝。
- `patches` 不允许任何远程目标；内联 patch 内容不涉及路径检查。
- `generators`/`transformers`：内联条目的顶层 `exec` 字段或
  `config.kubernetes.io/function` 注解中的 exec 声明一律拒绝。
- `helmCharts`：`repo` 必须是白名单内的 https 地址，且必须显式给出
  `version`。
- 多 kustomization 之间的本地引用形成环（含自引用）时整体拒绝。

## 使用方式

```go
catalog, err := netsec.NewFileCatalog(files) // files: 仓库枚举结果，含符号链接
if err != nil {
    return err
}
admitter := netsec.NewAdmitter(catalog, []string{"github.com"})

k, err := netsec.DecodeKustomization(parsed) // parsed: map[string]any
if err != nil {
    return err
}
if err := admitter.Validate(k); err != nil {
    var admissionErr *netsec.AdmissionError
    errors.As(err, &admissionErr)
    return admissionErr // 聚合的 Violation 列表可直接回写到 MR 检查
}

// 仓库内存在多个 kustomization 时，再做跨文档循环引用检测：
// nodes 的键是各 kustomization 的根相对路径。
if err := admitter.ValidateReferenceGraph(nodes); err != nil {
    return err
}
```

`Violation.Reason` 是稳定的机器可读标识（如 `ReasonPathEscapesRoot`、
`ReasonRefNotCommit`、`ReasonExecPlugin`、`ReasonReferenceCycle`），
`AdmissionError.HasReason` 可用于按类别处理。

## 验证

测试覆盖 `../` 逃逸、符号链接出根与链接环、分支/标签/缺省 ref、
exec 生成器/转换器、helm 仓库与版本、以及跨 kustomization 循环引用：

```sh
go test -race ./...
```

仓库里的 kustomization.yaml 由持续交付系统直接构建，攻击者提交的合并请求可以让 resources 指向仓库外的目录、固定到可变分支的远程地址，或启用会执行外部程序的生成器。请给Go构建网关实现可导入的 kustomization 准入包，输入已解析的配置、仓库根下的文件列表（含符号链接目标）和调用方的远程主机白名单：resources、bases、patches 里的本地路径规范化后必须落在仓库根内，符号链接解析后也要在根内；远程引用必须 https、主机在白名单内，且 ref 是 40 位十六进制提交号，分支、标签和缺省 ref 都拒绝；generators 与 transformers 里 exec 类插件或带 exec 字段的条目一律拒绝；helmCharts 的 repo 须在白名单内并给出 version；configMapGenerator 的 files 条目同样受路径规则约束；循环引用整体拒绝。用 ../ 逃逸、符号链接出根、分支引用、exec 生成器和循环引用验证。
