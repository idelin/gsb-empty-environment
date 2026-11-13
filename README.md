# SAML NameID注释截断

攻击者在 IdP 注册了形如 admin@victim.com.evil.com 的名字并在其中插入注释，签名有效，而有的解析器只取首个文本节点，读成 admin@victim.com。为Go SAML 消费端写可导入的 NameID 读取包：签名摘要按排除注释的规范化计算，注释可被改动而摘要不变；NameID 内含注释、CDATA 或多个文本子节点时一律拒绝，否则取完整文本；首尾空白拒绝。用注释分割、CDATA、纯文本和注释在元素之外验证。
