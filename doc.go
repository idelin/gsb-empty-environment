// Package netsec 为 Go 收信宿主提供发送域策略解释能力。
//
// 支持 ip4、ip6、all、include 机制与 redirect 修饰符，以及
// +、-、~、? 四种结果前缀。include 仅在子策略判定为 pass 时匹配，
// 子策略的临时错误向上传播；redirect 仅在所有机制均未匹配时执行。
// 单次评估内所有递归共享同一 DNS 查询预算，重复域名查询走缓存，
// 策略环判定为永久错误。
package netsec
