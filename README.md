# 通用链接components匹配

iOS 通用链接的 apple-app-site-association 里 components 的顺序和 exclude 决定哪些路径打开应用，攻击者用 /share/../account 或带查询参数的地址诱导打开受保护页面。为Go链接服务实现可导入的路径匹配包：调用方给出本应用的 appID，details 的 appIDs 为 TEAMID.bundle 形式，团队 ID 不符则忽略该项；components 逐条按顺序匹配，首条命中决定，exclude 为 true 则不打开应用；/ 字段用 * 匹配任意字符序列、? 匹配单个字符，? 字段匹配查询串，# 字段匹配片段；URL 路径先消去点段再匹配，%2F 与重复斜杠拒绝。用点段、exclude 顺序、通配与查询匹配验证。
