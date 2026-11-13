# 源码归档省略子模块对象

源码证明只绑定主仓库提交，归档器若按当前工作区收集文件，攻击者可替换子模块或忽略未检出的对象。给Go制品宿主实现可导入的源码闭包包：宿主提供不可变对象库，提交对象列出树摘要及父摘要，树项类型为blob、tree或gitlink；blob摘要为SHA256(固定blob标签||长度||内容)，tree摘要为SHA256固定tree标签及按路径字节序排列的(模式、名称长度、名称、对象摘要)序列，提交摘要同样按固定标签与字段编码。gitlink对象值是另一仓库提交摘要，并由调用方清单映射到仓库身份、允许路径和受信根；递归子模块深度不超过8，仓库/提交对环拒绝。构造源码清单时遍历所有可达blob，保留仓库身份、路径链、模式和摘要；同一工作区路径不得由两来源占用，symlink内容只作链接目标字节且不得作为父目录。缺对象、摘要不符、未声明gitlink或子模块提交不在受信根通过宿主给定的父提交图可达闭包时整体失败。用内存对象库测试子模块替换、嵌套gitlink、共享blob、环和工作区额外文件；最终清单树根只由对象闭包计算，不读取当前文件系统。

## 可导入 API

```go
store := netsec.NewMemoryStore()
// 宿主从不可变快照装载对象；生产环境不要按工作区收集文件。

policy := netsec.TrustManifest{Repositories: map[netsec.RepositoryID]netsec.RepositoryPolicy{
	"main": {
		TrustedRoots: []netsec.Digest{rootCommit},
		Submodules:   map[string]netsec.RepositoryID{"vendor/lib": "lib"},
	},
	"lib": {
		TrustedRoots: []netsec.Digest{libCommit},
	},
}}

manifest, err := netsec.BuildSourceClosure(store, policy, "main", rootCommit)
```

`SourceManifest.Entries` 按工作区路径排序，每项包含仓库身份、仓库内路径、工作区路径、完整路径链、模式、blob 摘要和内容长度。`SourceManifest.TreeRoot` 与 `ManifestRoot` 都只由 `Entries` 计算；builder 不接受工作区路径，也不读取文件系统。

## 固定编码

标签均以 NUL 结尾；`uint64be` 为大端 8 字节整数；名称和路径排序使用原始字节序。

- blob：`SHA256(BlobTag || uint64be(len(content)) || content)`
- tree body：每个条目依次写入 `mode || NUL || uint64be(len(name)) || name || digest(32)`，tree 摘要为 `SHA256(TreeTag || body)`
- commit body：`tree(32) || uint64be(parentCount) || parent(32)...`，commit 摘要为 `SHA256(CommitTag || body)`
- manifest root：`SHA256(ManifestTag || uint64be(entryCount) || 每个条目的仓库、路径、路径链、模式、类型、摘要和长度)`

规范模式为 `100644`（普通文件）、`100755`（可执行文件）、`120000`（symlink）、`40000`（tree）、`160000`（gitlink）。`gitlink` 的摘要是子仓库提交摘要，不是当前仓库中的对象。

## 信任与失败语义

- 每个仓库的 `TrustedRoots` 通过宿主提供的 commit 父摘要图计算可达闭包；子树提交必须在该闭包内。
- 子模块只允许出现在调用方声明的精确仓库路径；路径映射同时给出子仓库身份。
- 顶层提交为深度 0，最多允许 8 层 gitlink；递归栈中同一 `(仓库, 提交)` 对出现即拒绝。
- 工作区路径冲突、symlink 被当作父目录、非规范 tree、重复名称、缺对象、摘要不符、未声明 gitlink 或未受信提交都会使构建整体返回错误和空清单。

## 验证

运行 `go test ./...`。测试使用内存对象库并覆盖子模块工作区替换、嵌套 gitlink、共享 blob、仓库/提交环守卫、工作区额外文件、深度边界、缺对象、摘要不符、未声明/未受信 gitlink、symlink 和确定性清单树根。
