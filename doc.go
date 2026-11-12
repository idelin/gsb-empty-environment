// Package netsec 为安全运行时提供携带认证标签的指针封装。
//
// 设计要点：
//   - MAC 以接口形式注入（见 [MAC] 与 [HMACTagger]），标签通过
//     [Registry.Mint] 覆盖地址、对象代次、指针种类与调用上下文。
//   - [Registry.Derive] 对派生指针重新计算标签，且种类只能保留或向
//     更低权限收窄，数据指针无法提升为函数指针。
//   - 同一地址重新 [Registry.Mint] 会分配新代次并覆盖旧映射，旧代次
//     指针在门禁处得到 ErrStaleGeneration。
//   - [Registry.Retire] 让对象代次（含其全部派生指针）立即失效，
//     [Registry.UseForCall] 与 [Registry.UseForDeref] 是按种类区分的
//     使用门禁，杜绝认证上下文复用时数据指针被当作函数指针调用。
package netsec
