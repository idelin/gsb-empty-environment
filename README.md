# 块完整性树局部更新

安全块存储更新一小段数据时，摘要树不能短暂包含新叶与旧父。实现供Go存储宿主调用的写时复制完整性树包，固定扇出和叶长由输入给定，事务可更新不相邻叶，提交只切换根；旧读者持有旧根，所有读取先验路径再交付字节。用逐次写失败、奇数叶数和共享祖先验证路径证明与旧快照不变。

## 用法

```go
tr, _ := netsec.Open(netsec.Config{Fanout: 4, LeafSize: 4096, NumLeaves: 1024})

snap := tr.Snapshot()          // 旧读者钉住旧根

tx := tr.Begin()               // 事务可更新不相邻叶
_ = tx.Set(3, blockA)
_ = tx.Set(900, blockB)
root, err := tx.Commit()       // 新节点先落 Store，然后只切换根

data, err := snap.Read(3)      // 先验路径再交付字节，仍读到旧版本
proof, _ := tr.Prove(3)        // 新根下的路径证明
ok := netsec.VerifyProof(root, blockA, proof)
```

- 写时复制：更新只重建受影响路径，未触碰子树在新旧根之间共享。
- 提交原子性：`Store.Put` 任一失败即中止，根不动，旧快照继续有效。
- 奇数叶数：每层末尾节点子节点不足扇出时按实际子节点数哈希。

测试：`go test -race ./...`
