// Package netsec 提供网关响应出口可调用的页面嵌入（点击劫持防护）策略判定。
//
// 入口为 Evaluate：传入响应头、被嵌入页面自身 URL 与从外到内的完整祖先链，
// 即可得到是否允许嵌入的结论。判定遵循以下规则：
//
//   - 强制 Content-Security-Policy 中存在 frame-ancestors 时，完全忽略
//     X-Frame-Options；Content-Security-Policy-Report-Only 不参与判定。
//   - 同名 CSP 头出现多次时，各 frame-ancestors 来源集合取交集；单条策略内
//     多个来源为“或”关系，且 'none' 与其他来源共存时 'none' 无效。
//   - 来源支持 'none'、'self'、纯方案（https:）、主机（可带 *. 通配前缀、
//     显式端口或端口通配 *）；畸形来源表达式按 CSP 规则忽略。
//   - X-Frame-Options 只接受恰好一个 DENY 或 SAMEORIGIN；重复头、逗号多值或
//     未知指令一律视为无效并按拒绝嵌入处理。
//   - 祖先链上每一层祖先都必须匹配，任一层不匹配即拒绝，而非只看直接父页面。
package netsec
