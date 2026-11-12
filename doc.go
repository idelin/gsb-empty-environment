// Package netsec 为 QEMU QMP 监控套接字代理提供命令级授权。
//
// 授权决策基于三类输入：
//   - 对端凭据：Unix 套接字 SO_PEERCRED 得到的 uid/gid/pid，以及套接字文件的权限位；
//   - 命令 JSON：QMP 请求的 execute、arguments、id 字段；
//   - 角色策略：每个角色的能力（admin、只读、允许目录等）以及凭据到角色的映射。
//
// 同一对端的命令流必须通过同一个 Authorizer 实例判定，以便跟踪
// qmp_capabilities 能力协商状态；批量消息通过 AuthorizeBatch 逐条
// 独立判定，单条被拒不影响后续命令。
package netsec
