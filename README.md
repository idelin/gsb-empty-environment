# bptree — B+ 树级联收缩与物理合并（Go，仅标准库）

基于页面（page）的内存 B+ 树，实现删除时的借调（borrow）与物理合并（merge）：

- **借调优先**：节点下溢（键数 < `MinKeys`）时，先尝试经父节点从左/右兄弟借调。
- **物理合并**：兄弟均处于最小占用无法借调时，将当前页与兄弟页合二为一
  （内部节点会把父节点分隔键拉下），从父节点删除索引项，空出的页回收至空闲页链表。
- **级联收缩**：父节点因此下溢则递归向上合并；根节点仅剩一个子节点时，
  子节点晋升为新根，树高减一，旧根页回收。
- **空闲页链表**：`pager.freeList` 回收所有合并/根收缩释放的页，后续插入优先复用。

## 运行测试

```sh
go test -v ./...
```

验收用例 `TestCascadingMergeShrinksTree`：500 条记录的满树（高 4 层）逐一删除至剩 3 条，
树高 4→3→2→1 安全缩减至 1 层，152 个空闲页全部回收至空闲页链表（仅余 1 个根叶页）。
每次删除后校验结构不变量（有序性、占用上下界、叶层齐平、分隔符界、页账目无泄漏）。

## 运行样例

```sh
go run ./cmd/demo
```

输出：

```
built full tree: keys=500 height=4 livePages=153 freePages=0
deleted 281 keys: height 4 -> 3 (livePages=63 freePages=90)
deleted 457 keys: height 3 -> 2 (livePages=10 freePages=143)
deleted 493 keys: height 2 -> 1 (livePages=1 freePages=152)
final: remaining=[498 499 500] height=1 livePages=1 freePages=152 created=153 mergedFreed=152
```
