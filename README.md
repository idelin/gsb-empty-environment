# Composer安装路径规范化

安装器按包 type 和 extra.installer-name 把包放到项目目录，攻击者给出 ../../ 或绝对路径的 installer-name，或用大小写与保留名让两个包落到同一位置。为Go安装组件实现可导入的安装路径解析包：路径模板只支持 {$vendor}、{$name}、{$type}，名称用小写化后的 vendor/name，installer-name 只许 [a-z0-9._-] 且不得为 . 或 ..；解析后的绝对路径必须仍在项目根内，经过已有符号链接后同样成立；Windows 保留名（CON、NUL、COM1 等）与不同大小写重名者按冲突拒绝。用穿越、符号链接、保留名和大小写冲突验证。

## installpath 包

可导入的安装路径解析器位于 `installpath`，它把“包 + 路径模板”解析为项目根内的
绝对安装路径，并对每一步输入做安全校验。

```go
import "example.org/netsec/q0255/installpath"

resolver, err := installpath.NewResolver("/var/www/project", "{$type}/{$vendor}/{$name}")
if err != nil { /* 模板或项目根非法 */ }

target, err := resolver.Resolve(installpath.Package{
    Vendor:        "Acme",
    Name:          "Widget",
    Type:          "library",
    InstallerName: "widget", // 可选，对应 extra.installer-name
})
// target = /var/www/project/library/acme/widget
```

### 安全保证

- **模板白名单**：模板必须是相对路径，仅允许 `{$vendor}`、`{$name}`、`{$type}`
  三个占位符，任何其它 `{...}`、绝对路径、反斜杠或 `.`/`..` 段一律拒绝。
- **名称小写化**：`vendor`、`name` 先转小写再校验与渲染；`installer-name` 存在时
  覆盖 `{$name}`，但必须完全匹配 `[a-z0-9._-]`，且不能是 `.` 或 `..`，也不能
  包含路径分隔符或盘符（如 `C:`）。
- **词法边界**：渲染结果先 `Clean`，再用 `filepath.Rel` 确认目标就是项目根本身或
  其严格子路径，挡掉 `../../` 与绝对路径穿越。
- **符号链接边界**：从项目根逐段 `Lstat`/`EvalSymlinks`，对已存在的组件解析真实
  路径；解析后的真实路径仍必须位于已解析的项目根内，防止借已有软链逃逸。
- **Windows 保留名**：任意路径段命中 `CON`、`PRN`、`AUX`、`NUL`、`COM1..9`、
  `LPT1..9`（含带扩展名/ADS 的形式，如 `NUL.txt`、`aux:stream`）即拒绝；以点或
  空格结尾的段也一并拒绝。
- **冲突拒绝**：冲突键取真实路径的小写形式；不同包（仅大小写不同也算不同包）
  解析到同一位置返回 `ErrConflict`，同一个包重复解析则幂等返回。

### 错误

所有失败都返回包装过的哨兵错误，用 `errors.Is` 判断：
`ErrTemplate`、`ErrPackage`、`ErrInstallerName`、`ErrReservedName`、
`ErrEscape`、`ErrConflict`。

### 验证

`installpath/installpath_test.go` 覆盖路径穿越（`../`、绝对路径、盘符、UNC）、
符号链接逃逸与内部软链、Windows 保留名、大小写重名冲突，以及并发解析
（`go test -race ./...`）。
