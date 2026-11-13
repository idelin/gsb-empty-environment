# 压缩成员证明里的旁支替换

密钥目录把多个成员证明压成一份后，恶意目录可塞入未使用的旁支或替换缺席证明。给Go客户端实现稀疏Merkle树多证明包：键为64位无符号数，构造高度h为1至64且键小于2^h，服务使用h=64，路径按h个有效位的最高位先走；存在叶为SHA256(00||键的大端8字节||值长的大端8字节||值)，缺席叶为SHA256(02)，内部为SHA256(01||左摘要||右摘要)，00、01、02均为单字节。查询键须严格递增且1至256个，旁支用深度和该深度的前缀定位，只能位于查询路径的兄弟边界；未提供旁支视为该层空子树。拒绝重复坐标、前缀多余位非零、显式空旁支、覆盖查询的旁支以及任何未消费节点。实现从稀疏键值表生成证明、按可信根验证存在或缺席、以及验证旧值后计算一批更新的新根；删除等同缺席，整批失败不产生新根。用h≤6且叶值取两个固定字节串的完整树参考穷举根与证明，篡改值、存在标记、旁支坐标及查询顺序测试不能伪造成员关系。

## 客户端 API

```go
tree, err := netsec.NewTree(netsec.ServiceHeight, entries)
root := tree.Root()

proof, err := tree.Prove([]uint64{10, 42, 99}) // 必须严格递增
err = netsec.VerifyProof(root, proof)           // 任一存在/缺席声明不匹配即失败

newRoot, err := netsec.UpdateRoot(root, proof, updates)
// updates 中的键必须都已被 proof 覆盖；任一错误时 newRoot 为零值。
// netsec.DeleteUpdate(key) 或 SetUpdate(key, value) 构造更新。
```

旁支坐标为 `(Depth, Prefix)`：根深度为 0，旁支深度取值 `1..h`；`Prefix` 是该深度路径前缀的数值，占结果低 `Depth` 位，可用 `PrefixAtDepth(key, h, depth)` 计算。未提供某层兄弟旁支时按该层空子树处理。`MultiProof.MarshalBinary`/`UnmarshalBinary` 使用严格长度校验和尾随字节拒绝，可信根不在编码内，需由调用方另行传入。
