# json-path-records

大型 JSON 树的流式路径记录格式 + 版本化重建存储（TypeScript，Node ≥ 23.6，仅用内置模块）。

## 线格式（wire format）

树被描述为记录流，每个节点一条记录：

```
record := varint(pop) varint(nseg) seg* op value?
seg    := 0x00 varint(len) utf8     -- 对象键（键永远是字符串，{"0":"x"} 不会变成数组）
        |  0x01 varint(index)       -- 数组下标
op     := 0 Null | 1 Missing | 2 Hole | 3 False | 4 True
        |  5 Num(f64 LE) | 6 Str(varint len + utf8) | 7 Obj | 8 Arr
value  := 仅 Num / Str 携带
```

- 路径相对上一条记录做增量编码（pop + 追加），深度 D 的链总开销 O(D)；解码器对路径做 interning（cons 链表结构共享），不会复制路径数组。
- **空容器**：Obj/Arr 记录 + 无子记录，即 `{}`/`[]` 显式可往返。
- **null**：`Null` 操作，与 `Missing`（墓碑，仅补丁内合法）严格区分。
- **数组洞**：`Hole` 记录，重建为真实 elision（`1 in arr === false`），长度保留。
- **数字对象键**：键段与下标段是不同标签，`{'0':'x'}` 恒为对象。
- 记录可任意分块传输（`RecordDecoder.push` 是增量状态机）；对象键无序（提交时规范化排序），数组有序。

## 重建与版本发布（src/store.ts）

1. `feed` 接收**乱序**记录，内存缓冲超过 `spillThreshold` 即排序后**溢写**为 `.run` 文件；
2. `commit` 对所有溢写 run + 内存缓冲做 k-way 归并，边流式边验证：
   父子类型（标量下出现子路径、对象内下标、数组内键）、重复定义、引用完整性（父缺失）；
   所有错误抛 `BuildError`，`err.at` 定位到具体路径（如 `$.a.b`）；
3. 验证与组装通过后才写 `<id>.idx`（tmp + fsync + rename），最后**原子发布** `ROOT.json`。
   崩溃恢复时 `ROOT.json` 是唯一事实来源：未发布的孤儿索引直接清除，绝不暴露半棵树。
4. 已提交版本不可变：移动子树前打开的前缀游标仍读旧版本。
5. `moveSubtree` 产生最小差量（1 条墓碑 + 移动子树记录）并返回**逆补丁**；
   `applyPatch` 以最顶层补丁路径整体替换子树语义应用；失败不改动原树。
6. `queryPrefix` / `materializePrefix` 在版本索引上流式做前缀查询，不全量物化。

## 运行测试

```
nvm use 23.9.0   # 或任何 >= 23.6 的 node（原生 type stripping + node:test）
npm test
```

## 实际运行结果（2026-09-25，node v23.9.0，macOS arm64）

```
$ node --test test/format.test.ts test/store.test.ts
✔ {'0':'x'} stays an object, never becomes an array (5.3ms)
✔ empty containers, null, holes and missing are encoded explicitly (11.9ms)
✔ arbitrary chunking round-trips node-by-node (10.5ms)
✔ non-JSON values are rejected with their path (1.6ms)
✔ depth 100k is handled iteratively; chunked round-trip is node-by-node equal (2754.3ms)
✔ child path under a scalar parent conflicts; failed commit leaves the tree untouched (44.8ms)
✔ duplicate definitions are rejected with their path (7.4ms)
✔ reference integrity: a child without its parent is rejected at the parent path (4.1ms)
✔ container type conflicts are located: index into object, key into array (2.4ms)
✔ tombstones are patch-only and rejected in a full build (0.6ms)
✔ prefix cursor opened before a subtree move keeps the old version; inverse patch restores (72.3ms)
✔ failed patches and illegal moves never change the original tree (22.0ms)
✔ crash after index write but before root publish recovers without exposing a half tree (16.1ms)
✔ memory pressure spills sorted runs to disk; out-of-order arrival still commits correctly (58.0ms)
✔ object key order is canonicalized, array order is significant (49.8ms)
✔ prefix query streams relative rows and materializes subtrees (14.7ms)
ℹ tests 16
ℹ pass 16
ℹ fail 0
ℹ duration_ms 3089.9
```

## 已知边界

- 提交时的归并/校验对每条记录做 O(depth) 的路径比较，面向"宽而中等深度"的树；
  十万级深度的链走格式层流式管线（编码→分块→解码→组装，全程 O(depth) 迭代），
  该路径有专门测试覆盖。
