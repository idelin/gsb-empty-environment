// Package netsec 为挂载服务提供 idmap 挂载请求的安全校验原语。
//
// idmap 挂载会把共享文件系统暴露给非初始用户命名空间。若不校验，请求者可在
// 授权范围外伪造主机 uid，并以映射内的 uid 0 制造带 setuid 位或文件能力
// （security.capability）的文件，待映射外的主机进程执行后完成提权。
//
// 本包在挂载服务真正建立映射前强制：
//   - 映射区间完整落在请求者被授权的主机 uid/gid 范围内；
//   - 条目之间在容器侧与主机侧均不重叠；
//   - 所有 first+count 运算在 uint64 下进行，拒绝 uint32 回绕；
//   - 非初始命名空间把 uid 0 映射出来的挂载必须携带 MS_NOSUID；
//   - security.capability 中 vfs_ns_cap_data 的 rootid 随映射双向翻译，
//     未被映射覆盖时拒绝而非放行。
package netsec
