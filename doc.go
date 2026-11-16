// Package netsec 提供 gRPC 服务端的服务暴露门控拦截器：
// 反射、channelz、admin、testing 与健康检查等管理类服务只对
// 内部监听上的连接可见，对外表现为服务不存在（UNIMPLEMENTED）。
package netsec
