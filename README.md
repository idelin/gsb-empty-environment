# 合约升级把旧存储读错了

可升级合约替换实现合约后沿用旧存储布局，攻击者触发字段错位，把管理员槽位读成用户余额。请实现供Go合约升级服务调用的可导入存储布局组件。升级前核对存储槽位、类型与继承顺序，布局变更需显式迁移；管理员与实现地址存放在不可冲突的槽位。升级后回读关键字段。用布局错位、槽位冲突、未迁移升级、合法升级和回读验证状态解释一致。

## 组件

包 `netsec` 为纯标准库实现，无外部依赖，升级服务直接导入即可。

- `LayoutBuilder`：按 Solidity 打包规则与 C3 继承顺序编译存储布局（槽位、偏移、类型、声明合约）。
- `Plan`：升级前核对槽位、类型与继承顺序，输出统一状态报告；阻断时返回 `ErrUpgradeBlocked`。
- `Migration`：显式迁移计划，支持 `MOVE` / `CLEAR` / `INITIALIZE`，引用不存在字段会被拒绝。
- `Apply` / `ApplyAndVerify`：执行迁移并切换 EIP-1967 实现槽位，管理员槽保持不变。
- `ReadbackVerify`：回读管理员、实现槽与关键字段，独立推导预期值并逐项比对。
- `AdminSlot` / `ImplementationSlot` / `BeaconSlot`：EIP-1967 keccak 命名空间槽，业务字段占用即报冲突。

## 统一状态

| 状态 | 含义 | 是否阻断 |
| --- | --- | --- |
| `LAYOUT_MISALIGNED` | 已使用槽位上字段位置/类型变化或继承顺序被打乱 | 仅在未被迁移覆盖时阻断 |
| `SLOT_CONFLICT` | 同槽字节区间重叠，或占用 EIP-1967 保留槽 | 是 |
| `UNMIGRATED_UPGRADE` | 需迁移的布局变更缺少显式迁移 | 是 |
| `LEGAL_UPGRADE` | 布局兼容且变更全部由显式迁移覆盖 | 否 |
| `READBACK_VERIFIED` | 回读完成，`ReadbackReport.Passed` 表示通过与否 | 失败由 `Passed=false` 表达 |

## 典型用法

```go
report, readback, err := netsec.ApplyAndVerify(
    storage, oldLayout, newLayout, adminAddr, newImplAddr,
    []netsec.Migration{
        {Op: netsec.MigrateMove, Field: "balance", FromField: "balance"},
        {Op: netsec.MigrateInitialize, Field: "nonce", Value: big.NewInt(0)},
        {Op: netsec.MigrateClear, Field: "paused"},
    },
)
if errors.Is(err, netsec.ErrUpgradeBlocked) {
    // report.Findings 给出 LAYOUT_MISALIGNED / SLOT_CONFLICT / UNMIGRATED_UPGRADE 解释
}
```

## 验证

`go test ./...` 覆盖 keccak 测试向量、EIP-1967 槽位常量、打包与冲突检测、
错位/冲突/未迁移/合法升级五状态，以及升级后回读失败捕获。
