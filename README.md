# 带时效窗口的流式风控规则引擎

TypeScript 实现的流式风控决策组件:规则条件组合**单笔交易特征**与**滑动窗口历史频次**,
支持规则版本**动态平滑热切换**——已开启的活跃会话钉住旧版本平稳过渡,新会话严格按新版本裁定,
每笔决策写入带规则版本号的审计日志。

## 运行环境

- Node.js >= 22.6(使用内置 `--experimental-strip-types` 直接运行 TypeScript,无需编译步骤)
- 测试使用 Node 内置测试框架 `node:test`,零第三方依赖

```bash
npm run demo   # 运行验收场景示例
npm test       # 运行 node:test 测试
```

## 设计

| 模块 | 职责 |
| --- | --- |
| `src/engine.ts` | `RiskEngine`:会话管理、规则评估、窗口事件落库、审计写入 |
| `src/windowStore.ts` | `WindowStore`:按 `(主体, 动作)` 查询 `(now-windowMs, now]` 内的频次与金额合计 |
| `src/registry.ts` | `RuleRegistry`:按 `effectiveFrom` 排序的版本化规则集,支持按时间点解析生效版本 |
| `src/auditLog.ts` | `AuditLog`:追加式审计日志,每条记录携带 `ruleVersion` |
| `rules/versions.ts` | 示例规则集 v1 / v2(v2 阈值收紧) |

### 规则模型

一条规则 = 单笔条件(`tx`:金额阈值、动作类型、特征等值) + 若干窗口条件
(`windows`:滑动窗口内次数/金额阈值),全部满足即命中,按 `priority` 从高到低首个命中者生效,
无命中默认 `ALLOW`。示例规则 R1(v1):`10分钟内转账超3次 且 金额超1万 -> BLOCK`。

### 版本热切换与会话钉住

- `publishRules(ruleSet)`:新版本按 `effectiveFrom` 生效,无需停机,即热切换。
- `beginSession(sessionId, at)`:会话开启时钉住当时生效的规则版本;长事务存续期间
  规则库更新不影响其裁定口径,实现平稳过渡。
- 未显式开启会话的请求,按首笔交易时间自动解析生效版本。
- 窗口统计口径:计入当前交易**之前**的历史事件(当前交易在评估完成后落库)。

## 验收场景(t 单位为分钟)

1. `t=0` 规则集 v1 生效(10分钟内转账>3次 且 金额>10000 -> BLOCK)。
2. `t=90` 长事务会话 `S-long` 开启,锁定 v1;`t=92~94` 产生 3 笔小额转账。
3. `t=100` 规则库热切换为更严格的 v2(10分钟内转账>2次 且 金额>5000 -> BLOCK)。
4. `t=101` `S-long` 提交 8000 元转账:按旧规则 v1 裁定 -> **ALLOW**,审计标记 `ruleVersion=v1`。
5. `t=101` 全新会话提交相同画像的 8000 元转账:按新规则 v2 裁定 -> **BLOCK**,审计标记 `ruleVersion=v2`。

`npm run demo` 输出:

```
[t=101] S-long 提交 8000 元转账 -> ALLOW (规则版本 v1)
[t=101] 新会话 S-b2 提交 8000 元转账 -> BLOCK (规则版本 v2)

=== 审计日志 ===
#6 t=101  tx=tx-a4 session=S-long  amount=8000  decision=ALLOW  rule=-   ruleVersion=v1
#7 t=101  tx=tx-b4 session=S-b2    amount=8000  decision=BLOCK  rule=R1  ruleVersion=v2
```

## 测试

`npm test`(node:test,6 个用例全部通过):

- 滑动窗口频次/金额统计与窗口边界(左开右闭)
- 规则注册表按时间点解析版本、拒绝重复发布
- 单笔特征与窗口频次组合条件(仅单边超限不命中)
- **验收边界**:t=90 长事务按 v1 放行、t=101 新请求按 v2 拦截、审计标记不同版本号
- 热切换后新会话自动使用当前生效版本
- 规则优先级与默认放行
