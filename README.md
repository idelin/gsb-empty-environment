# vfs — 虚拟目录导入路径解析库

纯内存的虚拟 inode 图，用于解析导入风格路径，不触碰宿主文件系统。

## 规则

- 路径分隔符仅为 `/`；反斜杠只是普通名字字符。
- 点段（`.` / `..`）按词法消去；任何越过根的 `..` 一律拒绝（`ErrEscapesRoot`），不做静默截断。
- 符号链接按**目录身份（inode 指针）逐组件**解析，每次解析最多 40 次跳转（`MaxHops`）；重复经过同一符号链接 inode 判为链接环（`ErrLinkLoop`）。
- 包含性通过从结果 inode 沿父指针走回根来验证，**不做字符串前缀比较**。
- 替换（`ReplaceWithDir` / `ReplaceWithSymlink`）是 copy-on-write 原子换入新 inode；解析全程无锁，事后重新校验每一条经过的 (目录, 名字, 子 inode) 边，发现替换竞态即重试，绝不返回撕裂状态。

## 用法

```go
fs := vfs.New()
fs.MkdirAll("/a/b")
fs.Create("/a/b/f")
fs.Symlink("/a/b", "/link")

r, err := fs.Resolve("/link/f") // r.Path == "/a/b/f"，r.Ino 为真实 inode
```

## 测试

```sh
GO111MODULE=on go test -race ./...
```

覆盖：点段消除、根逃逸拒绝、根内跳转（绝对/相对/链式/指向根）、链接环、40 跳边界、inode 身份而非字符串前缀（`/real` vs `/realistic`、替换后 inode 变更）、以及两种替换竞态（符号链接互换、目录↔符号链接互换）。
