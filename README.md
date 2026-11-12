# in-toto材料闭合规则

攻击者想在两个供应链步骤之间悄悄替换中间产物。为Go发布网关实现可导入的in-toto layout规则求值包：每步materials按顺序执行MATCH…WITH PRODUCTS FROM、ALLOW和DISALLOW *，已被匹配的制品不得再次匹配，路径与sha256须同时相等。用改名保摘要、同路径换摘要和遗漏DISALLOW的样例测试。
