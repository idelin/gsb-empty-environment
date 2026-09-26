# datrie — 动态双数组 Trie（Go，仅标准库）

从空仓库实现的 base/check 双数组 trie，支持动态插入（含冲突重定位）、
删除（含自底向上剪枝与槽位回收）、前缀遍历。无任何第三方依赖，
索引进程中不使用 `map`——`map` 只出现在测试里作为对拍参照。

## 编码布局（真实占用）

四条平行数组，按下标（槽位号）对齐：

| 数组      | 类型     | 含义 |
|-----------|----------|------|
| `base[i]` | `int32`  | 节点有子节点时为基址 B，子节点沿字节 c 位于槽 `B+c`；0 表示暂无子节点（槽 0 是 root，永不作子槽，故 0 保留）；空闲槽中存放空闲链表的后继指针 |
| `check[i]`| `int32`  | 父槽号，弧可校验：`check[base[p]+c] == p`；`-1` 表示空闲槽；root 为哨兵 `-2` |
| `value[i]`| `int64`  | 终止于槽 i 的词的值（仅终止位有效时有效） |
| `term[]`  | `uint64` | 终止位图：第 i 位置位 ⇔ 有词在槽 i 结束——**词的前缀仍可作为词** |

每槽占用 4+4+8 字节 + 1/8 字节的终止位 ≈ **16.1 B/槽**，与 key 长度无关。
空闲槽通过 `base[]` 线程化（intrusive）的空闲链表回收复用。
`Stats()` 返回真实的槽数与字节数。

## 关键不变量

- 插入冲突时 `relocate` 为父节点的全部子节点（含新标签）寻找新基址，
  逐个搬移子槽：保留其 `base`/`value`/终止位，`check` 改写为父槽号，
  并把**所有孙节点的 check 从旧子槽改指到新子槽**，旧槽归还空闲链表，
  不产生孤儿槽。
- 重复插入只更新 `value`，不分配新槽（测试断言槽数不变）。
- 删除时清除终止位，并自底向上释放"无子且非终止"的叶槽，槽位回到
  空闲链表供后续插入复用。

## API

```go
tr := datrie.New()
tr.Insert("hello", 1)        // 重复插入仅更新值
v, ok := tr.Search("hello")
ok = tr.Delete("hello")      // 返回词是否存在
entries := tr.PrefixSearch("he") // 按字典序返回，前缀本身是词也包含
st := tr.Stats()             // 真实布局与字节占用
```

key 按字节处理，UTF-8 字符串（如 `"中文"`）天然支持。

## 运行样例

```
$ go run ./cmd/demo
== inserted 10 words (prefixes he/中 are words themselves) ==
slots=234 used=25 free=209 words=10
  base[]     936 B (234 slots x 4B)
  check[]    936 B (234 slots x 4B)
  value[]   1872 B (234 slots x 8B)
  term[]      32 B (bitset)
  total     3776 B (16.1 B/slot)

== search ==
Search("he") -> 1, true
Search("hello") -> 4, true
Search("hel") -> 0, false
Search("中文") -> 9, true

== duplicate insert updates value in place ==
hello: value=44, slots 234->234 (no orphan slots)

== prefix traversal ==
  he       1
  hear     2
  heard    3
  hello    44
  help     5
  中        8
  中国       10
  中文       9

== delete hello (prefix he and sibling help survive) ==
hello found=false, he=1(true), help=5(true)
slots=234 used=23 free=211 words=9
```

## 测试结果

```
$ go test -v ./...
=== RUN   TestInsertAndSearch
--- PASS: TestInsertAndSearch (0.00s)
=== RUN   TestPrefixOfWordIsWord
--- PASS: TestPrefixOfWordIsWord (0.00s)
=== RUN   TestDuplicateInsertUpdatesValueNoOrphans
--- PASS: TestDuplicateInsertUpdatesValueNoOrphans (0.00s)
=== RUN   TestRelocationKeepsChildren
--- PASS: TestRelocationKeepsChildren (0.00s)
=== RUN   TestDelete
--- PASS: TestDelete (0.00s)
=== RUN   TestDeletePrunesAndRecyclesSlots
--- PASS: TestDeletePrunesAndRecyclesSlots (0.00s)
=== RUN   TestPrefixSearch
--- PASS: TestPrefixSearch (0.00s)
=== RUN   TestEmptyKey
--- PASS: TestEmptyKey (0.00s)
=== RUN   TestUTF8Keys
--- PASS: TestUTF8Keys (0.00s)
=== RUN   TestRandomAgainstMap
random test: 1101 words, {Slots:1662 Used:1451 Free:211 Words:1101
    BaseBytes:6648 CheckBytes:6648 ValueBytes:13296 TermBytes:208 TotalBytes:26800}
--- PASS: TestRandomAgainstMap (0.91s)
PASS
ok  	datrie	1.224s
```

`TestRandomAgainstMap` 用小字母表随机 key 做 5000 次插入/删除对拍
（参照为 `map`，仅存在于测试中），每次操作后校验：空闲链表与
`check==-1` 完全一致、每个使用中的槽都能被父节点指回、从 root 可达的
槽数等于已用槽数（无孤儿）。

## 说明与取舍

- 子节点枚举与 `childrenOf` 按 256 字节字母表扫描，单节点 O(256)；
  查找/插入为 O(key 长度 × 256) 上界，实际冲突时才触发重定位。
- 数组按需逐槽增长，重定位回退策略把新块放到数组尾之后；生产实现
  可改为按块增长与更积极的空闲块搜索以压低 free/slots 比。
