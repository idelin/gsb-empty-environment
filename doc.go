// Package netsec 是认证回退路径覆盖任务的根包。
//
// 协议路径监视器实现在子包 pathmonitor 中：状态声明保证等级，边标注
// failure / recovery-proof / reauth / normal 语义，Check 枚举从起点到
// 全部成功出口的路径，报告最弱累计保证、降级路径、恢复环与异常出口，
// 并断言强认证失败后只能进入受限恢复流程，不得回落为普通登录成功。
package netsec
