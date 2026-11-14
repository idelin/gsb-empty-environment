# 会话规则被另一控制面改写

策略控制按会话标识下发规则，攻击者伪造控制面消息修改计费与转发规则，让流量免计费或改道。请实现供C++20策略控制组件调用的可导入会话规则组件。规则消息双向认证并绑定会话、用户与控制面节点，序列号单调；跨会话、旧序号与未授权节点拒绝。规则生效前回读确认。用规则篡改、序号回退、节点伪造、合法下发和回读验证规则只属本会话。

## 组件

`session_rules.hpp` / `session_rules.cpp` 提供可导入的 C++20 会话规则组件
`netsec::session_rule::SessionRuleEngine`，无第三方依赖（内置 SHA-256 / HMAC-SHA256）。

- 双向认证：控制面 -> 组件用节点入口密钥校验规则消息；组件 -> 控制面用会话出口密钥
  签名回读报文，两个方向独立密钥，任一侧均可验证对端。
- 强绑定：MAC 覆盖会话、用户、控制面节点、序列号与完整规则体（标签 + 长度的规范化
  编码，避免字段拼接歧义）。
- 节点授权：仅接受 `EngineIdentity::authorized_nodes` 白名单内节点，每节点独立密钥与
  序列号窗口。
- 单调序号：按节点拒绝旧序号（`StaleSequence`）与重复序号（`DuplicateSequence`）。
- 回读确认：规则先以 `Pending` 入库，`readback()` 生成签名回读，控制面
  `verifyReadback()` 核对后 `activate()` 置为 `Active`；未确认规则不会出现在
  `effectiveRule()` 中，无法影响计费/转发。
- 会话隔离：一个引擎只服务一个（会话，用户）对，跨会话、跨用户消息拒绝，规则不能被
  其他会话回读或激活。

### 调用流程

```cpp
netsec::session_rule::EngineIdentity id{
    .session_id = "session-A",
    .user_id = "user-1",
    .egress_key = "session-egress-key",
    .authorized_nodes = {{"pcf-primary", "node-ingress-key"}}};
netsec::session_rule::SessionRuleEngine engine(std::move(id));

netsec::session_rule::RuleMessage msg = /* 控制面用入口密钥签名 */;
if (auto r = engine.ingest(msg); !r) { /* 按 r.reason 拒绝并审计 */ }

auto report = engine.readback(msg.rule.rule_id);   // 组件签名的回读
if (engine.verifyReadback(*report, msg.rule)) {
    engine.activate(*report);                      // 确认后规则才生效
}
auto active = engine.effectiveRule(msg.rule.rule_id);
```

### 拒绝原因

`UnauthorizedNode`、`BadMac`、`SessionMismatch`、`UserMismatch`、
`StaleSequence`、`DuplicateSequence`、`UnknownRule`、`ReadbackMismatch`、
`AlreadyActive`。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

`test_session_rules.cpp` 覆盖：规则篡改（免计费/改道/过滤器）、序号回退与重放、
节点伪造与身份冒充、跨会话/跨用户绑定、合法下发与回读确认、回读报文篡改、规则只属
本会话，以及 SHA-256（FIPS 180-4）与 HMAC-SHA256（RFC 4231）已知答案向量。
