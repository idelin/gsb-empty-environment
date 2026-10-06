# fsdiff

inode 身份识别的目录树比较库。对同一棵目录树的两次快照（`Scan`）计算最小操作序列（`Diff`），
并可在虚拟 inode 树（`VTree`）上回放、校验结果与目标快照完全一致（`Verify`）。

## 核心规则

- **inode 即身份**：以 `(dev, ino)` 作为文件系统对象的稳定身份。路径变化而 inode 不变 → `move`。
- **内容不等于身份**：内容相同但 inode 不同，输出 `delete` + `create`，绝不误报为移动。
- **硬链接按链接集合比较**：一个 inode 的多个路径表示为 `(父目录 inode, 名称)` 集合；
  集合差分得到 `link` / `unlink`，无公共链接时首对报为 `move`。
- **目录移动不隐藏内部修改**：链接按结构（父 inode + 名称）而非绝对路径比较，
  目录移动只产生一条 `move`，子节点仍按各自 inode 独立匹配，内部修改照常输出 `modify`。

## 回放（VTree）

`Op` 携带父目录 inode ID，回放与祖先重命名无关。`Apply` 分三阶段：
删除/解链（深→浅）→ 创建/链接/移动挂接（浅→深）→ 内容修改。
交叉移动（`a`↔`b` 互换）无需临时名即可安全回放。`Verify` 重放后比较
inode 集合、状态、路径集合与目录项，确认与目标快照完全一致。

## 用法

```go
oldSnap, _ := fsdiff.Scan("/path/before")
newSnap, _ := fsdiff.Scan("/path/after")
ops := fsdiff.Diff(oldSnap, newSnap)   // create/delete/move/link/unlink/modify
err := fsdiff.Verify(oldSnap, newSnap, ops)
```

## 测试

```sh
go test ./...
```

覆盖：交叉移动（文件/目录互换）、同内容不同 inode、同路径替换、硬链接增删、
目录移动 + 内部修改、组合场景、篡改检测。
