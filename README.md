# tRPC过程路径与批量授权

tRPC 风格的路由用点分路径找到过程，批量请求把多个调用放进同一次 HTTP，攻击者用特殊路径名触达原型属性、把受保护调用混进批量，或用 GET 触发变更。为TypeScript服务写可导入的过程分发包：过程只能按自有属性查找，路径段为 __proto__、constructor、prototype 一律返回 NOT_FOUND；批量请求的每个元素独立鉴权，一个元素 UNAUTHORIZED 不影响其他元素，且错误里不含其他元素的输入；mutation 与 subscription 经 GET 调用返回 METHOD_NOT_SUPPORTED；批量元素数超过 10 拒绝；输入 JSON 里重复键拒绝，受保护过程在解析输入之前先鉴权，鉴权失败时不得泄露输入校验信息。测原型路径、混合批量、GET 变更、先鉴权后解析。
