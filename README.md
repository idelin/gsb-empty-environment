# frame-ancestors与X-Frame-Options并存判定

页面同时带 X-Frame-Options 与 CSP frame-ancestors 时，不同浏览器的取舍不同，攻击者借重复头或畸形来源表达式让页面可被嵌入，实施点击劫持。为Go网关写可导入的嵌入策略判定包，供响应出口调用：存在 frame-ancestors 时忽略 X-Frame-Options；frame-ancestors 的来源列表支持 'none'、'self'、方案、主机（可带 *. 通配前缀与端口），'none' 与其他来源共存时 'none' 无效、其他来源仍生效；同名 CSP 头出现多次时取全部策略的交集；Report-Only 策略不参与判定；X-Frame-Options 只认 DENY 与 SAMEORIGIN，多个值（含逗号）或重复头视为无效，本题规定按拒绝嵌入处理；嵌套祖先链上每一层祖先都须匹配，不只检查直接父页面。用 X-Frame-Options 被忽略、'none' 共存、Report-Only、重复 X-Frame-Options 和三层嵌套的中间层验证。
