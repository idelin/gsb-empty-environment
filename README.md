# B+ 树级联收缩与物理合并（Go 版）

基于固定大小页面的 B+ 树，实现删除时的**借调（再分配）→ 物理合并（Merging）→ 根收缩**完整级联逻辑。仅使用 Go 标准库。

## 设计

- 页面容量：`MaxKeys = 4`，非根页最小键数 `MinKeys = 2`，内部节点最大扇出 5。
- `pager` 模拟磁盘页管理器：页面按 ID 寻址，释放的页面进入**空闲链表（Free Page List）**并 LIFO 复用。
- `BulkLoad` 批量构建满树：500 条记录 → 125 叶子页 + 31 内部页 = 156 页，树高 4。
- `Delete` 删除流程：
  1. 叶子删除键后若下溢，优先从左/右兄弟**借调**（兄弟键数 > MinKeys 时），并更新父节点索引项；
  2. 兄弟均无法借调时执行**物理合并**：当前页与兄弟页合二为一，从父节点删除索引项，被清空的页回收至空闲链表；
  3. 父节点因此下溢则**递归向上合并**；内部根只剩一个孩子时，孩子升格为新根，旧根页回收，树高减一；
  4. 回溯时刷新因最小键变化而过期的祖先索引项。

## 运行样例

```console
$ go run ./cmd/demo
批量加载 500 条记录：树高=4，在用页面=156，空闲链表=0
开始逐一删除 1..497 ...
  删除键 401 后：树高 4 -> 3（在用页面= 31，已回收=125，合并=124，借调=248）
  删除键 481 后：树高 3 -> 2（在用页面=  6，已回收=150，合并=148，借调=296）
  删除键 497 后：树高 2 -> 1（在用页面=  1，已回收=155，合并=152，借调=304）
删除完成：
  剩余键        = [498 499 500]
  树高          = 1
  在用页面      = 1
  空闲链表页面  = 155
  物理合并次数  = 152
  兄弟借调次数  = 304
结构校验：通过
```

## 测试结果

```console
$ go test ./... -v
=== RUN   TestBulkLoadFullTree
--- PASS: TestBulkLoadFullTree (0.00s)
=== RUN   TestCascadingMergeShrinksTree
--- PASS: TestCascadingMergeShrinksTree (0.00s)
    btree_test.go:103: height path [4 3 2 1], merges=152 borrows=304, freed 155 pages to free list
=== RUN   TestCascadingMergeRandomOrder
--- PASS: TestCascadingMergeRandomOrder (0.00s)
=== RUN   TestDeleteMissingKey
--- PASS: TestDeleteMissingKey (0.00s)
=== RUN   TestFreeListReuse
--- PASS: TestFreeListReuse (0.00s)
PASS
ok  	bptree
```

## 验收边界覆盖

- `TestCascadingMergeShrinksTree`：500 条满树逐一删除至 3 条，断言树高严格沿 `4 → 3 → 2 → 1` 收缩，最终仅剩 1 个根叶页，155 个空余页全部进入空闲链表；每次删除后校验结构不变量与页面守恒（在用页 + 空闲页 = 历史总页数）。
- `TestCascadingMergeRandomOrder`：随机顺序删除同样收缩到 1 层、仅剩 1 页。
- `TestBulkLoadFullTree` / `TestDeleteMissingKey` / `TestFreeListReuse`：满树构建与查找、幂等删除、空闲链表复用。

## 文件

- `btree.go` — 分页器、批量加载、删除（借调/级联合并/根收缩）、结构校验
- `btree_test.go` — go test 验收测试
- `cmd/demo/main.go` — 运行样例
