// Package netsec 是一个 Cedar 风格的最小策略评估包，供 Go 授权服务在
// 请求路径上直接调用。
//
// 本包只实现题目要求的子集：permit/forbid 的
// (principal, action, resource) 三元组策略，条件表达式支持
// ==、!=、<、>、&&、||、has、属性访问 a.b 以及 in。
//
// 策略文本语法（分号可省略）：
//
//	permit principal action resource when <expr> ;
//	forbid principal action resource when <expr> ;
//
// 表达式示例：
//
//	principal.role == "admin"
//	principal has "tenant" && principal.tenant == resource.tenant
//	action.name in ["read", "view"]
//	resource.level < 5
//
// 求值与组合语义：
//   - 默认拒绝；
//   - 任一 forbid 条件求值为真即拒绝，forbid 优先于 permit；
//   - 条件求值出错（访问不存在的属性、在非记录上取属性、
//     类型不符等）时，该策略标记为 error 并视为不适用；
//   - 出错的是 forbid 且调用方设置 failClosed=true 时，整个请求拒绝；
//   - failClosed=false 时出错的 forbid 被跳过，只有存在求值成功且
//     条件为真的 permit 才放行；
//   - has 是受保护的属性存在性判断：principal has "x" 不会因为 x
//     缺失而出错，典型用法是 principal has "x" && principal.x == ...，
//     配合 && 的短路求值保护后续属性访问；
//   - ==/!= 两侧类型不同时直接判不等，不算求值错误；
//   - <、>、in、&&、|| 遇到不兼容类型属于求值错误。
//
// 请求属性使用 map[string]any 嵌套表达，集合使用 []any。
package netsec
