# DNS约束不含前导点

约束写成.example.com时被解释成只限制子域，example.com本身漏掉。带前导点的DNS约束直接无效；不带点的约束同时匹配该名和其子域。约束example.com能匹配a.example.com和example.com。可导入的Go库供证书路径调用。用带点的约束去匹配example.com本身。
