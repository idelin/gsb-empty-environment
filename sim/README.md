# 主从写入模拟器（基于任期的写入日志）

模拟客户端缓存旧主地址、切换后旧主仍收到写入的场景。

## 核心规则

1. **提交仅接受当前任期的授权者**：`Authority`（配置中心）记录当前
   `(term, primary)`。节点提交前调用 `AuthorizeCommit` 校验
   `node.Term == auth.Term && node.ID == auth.Primary`，旧主无法提交。
2. **延迟复制不得替换已提交前缀**：复制以 `(term, log, commitIdx)` 快照
   进行；合并时 `idx < commitIdx` 的条目必须一致，否则拒绝；冲突时仅
   截断**未提交**后缀。
3. **重复请求幂等**：客户端每次请求携带 `ReqID`，节点按 `ReqID` 去重，
   重发（即使值不同）直接返回首次结果。

## 运行

```sh
./run.sh            # 全部场景
./run.sh partition  # 场景1：分区，客户端缓存旧主地址
./run.sh recovery   # 场景2：旧主恢复，截断未提交后缀
./run.sh dup        # 场景3：延迟复制保护已提交前缀 + 重复请求幂等
```

## 输出图例

- 日志条目前 `*` 表示已提交（committed），空格表示未提交。
- 每步打印授权方 `(term, primary)` 与各节点的 `term / commitIdx /
  log / store` 可见值。
