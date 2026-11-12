# Seatbelt末次匹配判定

macOS 沙箱配置以 (deny default) 起头，规则按出现顺序评估且最后一条匹配者生效；攻击者借 subpath、literal 与正则的覆盖差异，或后面的宽泛 allow 覆盖前面的 deny 读取用户私密目录。为Go评审组件实现供配置发布前调用的判定包：规则含 allow 或 deny、操作名（file-read*、file-write*、process-exec）与过滤器 literal、subpath、regex（整串匹配）；操作名以 * 结尾时通配该前缀的所有操作；给定操作与路径返回结果，并检出被后续规则完全遮蔽的 deny。用顺序翻转、subpath 边界（/a/b 不含 /a/bc）、通配操作和被遮蔽的 deny 验证。
