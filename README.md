# archmerge — 多源归档合并工具

把多个源归档（目录）合并成一份**合并清单**，每项都带来源信息。
**只读源归档**，清单写到 stdout 或 `-o` 指定的文件。

## 用法

    archmerge [-o manifest.json] <source-archive-dir>...

- 退出码：0 无冲突；2 有冲突（清单照常生成，冲突项列在 `conflicts`）。
- 每个源归档根目录可放 `.archmerge.json` 提供版本号和墓碑：

```json
{
  "versions": {"path/to/file": 2},
  "tombstones": [{"path": "old/dir", "version": 5}]
}
```

未列出的路径版本为 0。文件摘要自动计算（sha256）。

## 合并规则

1. 同路径**版本高者胜**；低版本记入 `superseded`。
2. 同版本同类型同摘要：并列，来源全部列入 `sources`。
3. 同版本文件**摘要不同**：报 `digest-mismatch` 冲突，该路径不进清单。
4. 同版本**文件 vs 目录**：报 `type-mismatch` 冲突——目录与文件绝不隐式互换。
5. 同版本**墓碑 vs 实体内容**：报 `tombstone-vs-content` 冲突。
6. 墓碑胜出：该路径及**整棵子树**删除（记入 `deleted` / `suppressed`）。
7. 文件胜出：其父路径类型决定整棵子树——其他源在该路径下的所有条目
   被抑制（记入 `suppressed`，`reason=ancestor-is-file`）。
8. 冲突路径不阻塞子树，子路径独立裁决。
9. 空目录作为显式 `dir` 条目保留在清单中。

结果是输入源**多重集合**的纯函数：与输入顺序无关（由枚举测试保证）。

## 输出清单

```json
{
  "sources": ["s1", "s2"],
  "entries":    [{"path", "type", "version", "digest", "sources", "superseded"}],
  "conflicts":  [{"path", "reason", "candidates"}],
  "suppressed": [{"path", "source", "reason", "blockedBy"}],
  "deleted":    [{"path", "version", "sources", "superseded"}]
}
```

## 测试

    go test ./...

`merge_test.go` 用小树枚举核对：
- 路径集合 `{"a", "a/b"}` 上每路径 9 种可能（缺席 / 文件 2版本×2摘要 /
  目录 2版本 / 墓碑 2版本），共 81 棵树，全部 6561 对两两合并：
  验证顺序无关性、幂等性（自合并不变）与结构不变量
  （前缀阻塞完备、冲突路径不进清单、来源合法等）。
- 3000 组三源抽样 × 6 种排列：验证多源顺序无关性。
- 定向用例：空前缀冲突、空目录保留、墓碑删除/被更新内容击败、
  版本优先、摘要冲突、类型冲突。
