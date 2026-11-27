// Package scim 提供无第三方依赖的 RFC 7644 SCIM 2.0 安全组件：
//
//   - 递归下降 filter 解析器（ParseFilter），支持 eq/ne/co/sw/ew/pr/
//     gt/ge/lt/le、and/or/not、括号分组以及 attr[subFilter] 值路径；
//   - 可配置的 AST 深度 / 节点数 / 输入长度上限，阻断解析器 DoS；
//   - 敏感属性查询授权与隐式租户隔离谓词绑定（Engine.SecureFilter）；
//   - 通用 JSON 资源求值器（Match），可直接对接内存或自研存储；
//   - RFC 7644 PATCH add/remove/replace 路径解析（ParsePatchPath）、
//     readOnly/immutable schema 校验、特权属性 / 特权组边界、
//     跨租户 members 引用拦截，以及快照级原子回滚事务（Store.Transact）。
//
// 本包仅使用 Go 标准库。包位于模块根目录，包名为 scim，宿主导入：
//
//	import scim "formal.local/w27-4e139ba19e"
//
// 典型查询路径：
//
//	secured, err := engine.SecureFilter(caller, rawFilter)
//	// secured 恒为 (rawFilter) and tenantId eq "<caller.TenantID>"
//
// 典型 PATCH 路径：
//
//	updated, err := engine.ApplyPatch(ctx, caller, "Group", id, req)
//	// 任一操作越权或跨租户，整个 PATCH 原子回滚并返回可 errors.Is
//	// 判定的哨兵错误（ErrCrossTenantMember / ErrImmutableAttribute / ...）。
package scim
