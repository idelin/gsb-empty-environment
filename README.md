# OPA 数据根归属绑定

攻击者提交的策略包会把数据文档写进其他团队的路径。`bundle` 子包为 Go
策略服务提供可导入的 bundle 加载器，强制数据文档只能落在所属团队的根路径下。

## Bundle 格式

Bundle 是一个 tar 归档（可选用 gzip 压缩，按魔数自动识别），包含：

- `.manifest`：JSON 清单，字段为 `roots`（数据根的路径段列表）与
  `file_digests`（归档键到小写十六进制 sha256 的映射，键形如
  `/data/team-a/x.json`）。
- `data/...`：清单列出的数据文档。

## 安全契约

- **段前缀匹配**：root 按完整路径段判断归属，`a/b` 包含 `a/b/c`，但不包含
  `a/bc`，字符串前缀巧合不会越权。
- **文件必须在自身 roots 内**：任何文档逃逸出本 bundle 的 roots 即整体拒绝。
- **roots 不得相交**：不同 bundle 的 roots 若存在父子或相等关系则拒绝加载；
  空 root（`""`）是全局根，与任何 root 都相交。
- **sha256 强校验**：每个文件的摘要必须与清单一致，摘要不符、清单列出但归档
  缺失、归档多出未列出的文件，任一情况都整体拒绝。
- **路径硬化**：拒绝绝对路径、`..` 穿越、反斜杠以及符号链接/硬链接等特殊
  tar 条目。
- **原子切换**：加载失败不会改动任何已生效的 bundle，旧版本继续提供服务；
  同名 bundle 仅在新归档完整通过校验后才替换。

## 用法

```go
loader := bundle.NewLoader()

f, err := os.Open("team-a.tar.gz")
if err != nil { /* ... */ }
defer f.Close()

if _, err := loader.Load("team-a", f); err != nil {
    // 旧版本仍在生效，记录错误并拒绝本次发布
}

doc, owner, ok := loader.LookupDoc("/data/team-a/policies/x.json")
```

失败原因可用 `errors.Is` 对照 `bundle.ErrOutsideRoot`、`bundle.ErrRootOverlap`、
`bundle.ErrDigestMismatch`、`bundle.ErrMissingFile`、`bundle.ErrUnexpectedFile`
等哨兵错误判定。
