# Kaniko构建快照与运行时挂载泄漏

Kaniko 在构建容器内直接对根文件系统做快照，运行时挂入的凭据卷、服务账号令牌若被快照进镜像层就等于泄漏；攻击者控制构建步骤，可把这些挂载内的文件复制到会被快照的位置。为 Go 构建服务写供层提交前调用的泄漏判定包：输入 /proc/self/mountinfo 文本（字段按空格分隔，路径中的空格、制表符、换行与反斜杠以三位八进制转义，可选字段以 - 结束）、构建期间各层的文件变更清单（路径、内容摘要）与调用方登记的敏感源摘要；挂载点本身与其下全部路径视为运行时挂载，不得出现在层变更里；变更文件内容摘要与敏感源摘要相同则视为泄漏，即使路径不在挂载下；/kaniko 与 /proc 之类的内置忽略路径另行列出。用含空格挂载点、嵌套挂载、复制出的凭据文件和合规层验证。

## 包 API

包路径 `netsec`（module `example.org/netsec/q0635`），层提交前按如下方式调用：

```go
mountPoints, err := netsec.ParseMountPoints(mountinfoReader)
if err != nil {
    return err
}

checker := netsec.NewChecker(
    mountPoints,
    sensitiveSourceDigests,
    netsec.WithExtraIgnorePaths("/build/tmp"), // 可选
)

findings := checker.Check(layers) // 空切片表示本批层无泄漏
for _, finding := range findings {
    // finding.Kinds: mount_path 和/或 sensitive_digest
    // finding.MountPoint: 命中的最具体运行时挂载点
}
```

判定规则：

- `ParseMountPoints` 解析 mountinfo，定位独立 `-` 分隔字段，对挂载点做三位八进制
  转义解码（`\040` 空格、`\011` 制表符、`\012` 换行、`\134` 反斜杠），去重并校验
  绝对路径；格式错误返回带行号的错误。
- 挂载点本身及其下全部路径属于运行时挂载，出现在层变更中即报 `mount_path`；嵌套
  挂载按最长前缀匹配到最具体的挂载点。
- 变更文件摘要命中调用方登记的敏感源摘要即报 `sensitive_digest`，即使路径不在任何
  挂载之下（凭据被复制到可快照位置）；同一变更可同时命中两条规则。
- 内置忽略路径（`/kaniko`、`/proc`、`/sys`、`/dev`）独立维护，其下变更不参与判定，
  可通过 `WithExtraIgnorePaths` 追加。
- mountinfo 中根文件系统自身的 `/` 挂载是快照目标；存在其他挂载时不作为运行时挂载
  判定，显式只登记 `/` 时仍可做全文件系统保护。

## 验证

`go test ./...` 覆盖含空格/制表符/换行/反斜杠转义挂载点、嵌套挂载最长前缀匹配、
挂载内凭据与复制到挂载外的凭据文件、同变更双规则命中、内置与追加忽略路径、合规层
零误报以及畸形 mountinfo 错误处理。
