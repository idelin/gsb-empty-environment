# SVG脚本净化

头像上传后的 SVG 会内联到页面，攻击者可放 script、foreignObject、事件属性、混入空白或实体的 javascript: 链接，以及指向 data: 的 use 引用。为 TypeScript 上传处理器写 SVG 净化包：只保留允许的元素与属性，解码实体后再判断 URL 协议，样式里的 url() 和 @import 按外部引用拒绝；净化结果再次净化应不变。用常见 XSS 向量、大小写和命名空间变形及正常图标验证输出。
