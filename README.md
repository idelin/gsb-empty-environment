# Markdown里的链接协议没有被限制

文档站允许用户写Markdown，攻击者用`javascript:`或`data:text/html`链接在他人阅读时执行脚本，净化器只过滤了script标签。请实现供TypeScript文档渲染服务调用的可导入Markdown净化组件。解析为语法树后只保留声明标签与属性，链接与图片协议限定为http、https与相对路径，`javascript`、`data`、`vbscript`及控制字符变体拒绝；URL按浏览器规则规范化后再判定。编码绕过、实体嵌套、注释与畸形标签按同一规则处理，输出转义完整。用协议注入、实体编码、大小写、嵌套标签、合法外链和相对路径验证不产生可执行链接。
