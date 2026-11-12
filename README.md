# CardDAV地址簿属性可见性与查询泄漏

共享地址簿对只读成员隐藏 NOTE 和 X-PRIVATE- 开头的属性，攻击者仍可用 addressbook-query 的 prop-filter 与 text-match 对隐藏属性做猜测，或靠 limit 与匹配条数推断。给TypeScript CardDAV 服务写供 REPORT 处理器调用的可见性包：过滤条件只能作用于该调用者可见的属性，作用于隐藏属性时按该属性不存在处理；address-data 请求隐藏属性时按未请求处理；nresults 限制和截断提示只统计可见匹配的联系人，且隐藏属性的修改不得改变只读成员看到的 getetag。用针对 NOTE 的 text-match 猜测、limit 计数、改隐藏属性后的 etag 验证；多取与同步令牌不在本题范围内。
