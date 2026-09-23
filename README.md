# negproto — 特性协商协议（Go，仅标准库）

双方声明**版本范围**、**必需特性**与**可选特性**，协商器在联合约束空间中
选择双方均可满足的**最高兼容组合**，失败时给出拒绝原因与最小冲突条件子集；
成功结果以证书形式绑定后续解码。

## 模型

`negotiate.Profile` 描述一方声明：

- `FeatureDecl.Versions`：支持的版本区间 `[Min, Max]`（含端点）。
- `Required`：该方必需；**对方不支持或区间交集为空时协商必须失败**。
- `Priority`：可选特性的偏好权重（双方权重相加，可为负）。
- `Depends`：依赖，含版本条件 `Range`（目标版本要求）与 `When`
（仅当自身选中版本落在该区间时依赖生效）。
- `Conflicts`：互斥特性名（双方声明取并集，对称生效）。

## 目标函数（全局求解，非逐特性交集）

依赖、互斥与版本条件把特性耦合在一起，因此协商是对联合赋值空间的
分支限界搜索，按字典序最大化：

1. 选中可选特性的总偏好权重；
2. 选中版本号之和（越高越新）；
3. 确定性平局裁决（按特性名字典序，选中优于缺席，版本高者优先）。

`budget` 限制主搜索的节点数；**预算耗尽以独立的
`StatusBudgetExceeded` 报告（未决），绝不与不可行混淆**。核心（core）计算
的预算不足同样单独以 `Result.CorePending` 标记。

## 失败报告

- 拒绝原因 `Reasons`：人类可读的逐条说明（必需特性不被支持、区间交集为空等）。
- 最小冲突条件子集 `Core`：对声明条件做删除式极小化得到的 1-最小不可满足子集
  ——再移除其中任意一条，问题即可行。

## 证书与会话绑定

- `Certificate = (Selection, Summary)`，`Summary = SHA-256(双方 Profile 与
  Selection 的规范编码)`。`Verify` 重算摘要并完整校验选中组合。
- `Session` 的每次**重试与降级请求都必须绑定当前会话摘要**；降级会收窄双方
  该特性的版本上限并重新协商，**必需能力绝不会被悄悄移除**（降级导致必需特性
  不可满足时失败且会话不变）。参数变化产生新摘要，**旧证书无法用于解码新参数**
  （`UnmarshalCertificate` 校验摘要绑定）。

## 序列化（wire 包）

自定义支持范围：uvarint、zigzag varint、布尔、定长前缀字符串/字节串、
计数序列；硬上限 `MaxStringLen / MaxBytesLen / MaxCount / MaxDepth`。
解码是纯数据处理，**不执行宿主代码**（无反射、无类型分派）；所有长度在
分配前先对照硬上限与剩余字节数验证（每个元素至少 1 字节，计数超过剩余
输入即拒绝），嵌套深度受限，尾随字节报错。

## 参考核验

`negotiate/exhaustive_test.go` 对小域做**全组合暴力枚举**作为参考解，
与求解器在 600 组随机小域（依赖/互斥/版本条件混合）上逐一交叉核验：
可行性、最优分值与确定性选择必须完全一致。

## 运行

```sh
go test ./...        # 全部测试
go run ./cmd/negdemo # 运行样例
```

## 运行样例输出

```
== negotiate ==
status:    ok
selection: map[compression:2 telemetry:2 tls:3]
score:     weight=12 versions=7 (search nodes: 11)
summary:   21b46628a93c48c2
certificate verifies against both profiles

== bound decoding ==
decode with current session summary: ok
decode with foreign summary:         rejected (negotiate: certificate summary does not match session binding)

== downgrade (bound to session) ==
stale downgrade request:  rejected (negotiate: request not bound to current session summary)
downgraded: map[compression:2 telemetry:1 tls:3]
new summary: 9d9626dc8cd00475 (old 21b46628...)
old certificate can no longer decode the session: rejected
required features preserved across downgrade

== rejection with reasons and minimal conflict core ==
status: infeasible
  reason: feature "hardware-token" is required by picky but not supported by server
  reason: feature "tls" is required but version ranges [5..6] (picky) and [2..3] (server) do not intersect
  core:   server requires feature "tls"
  core:   server supports "tls" at [2..3]
```

## 测试结果

```
ok  	negproto/negotiate	3.4s	（含 600 组随机小域全组合交叉核验）
ok  	negproto/wire	0.5s	（长度/深度/截断/溢出/尾随字节攻击面）
```
