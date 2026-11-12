# SSB消息规范序列化与分叉判定

Secure Scuttlebutt 的每条消息由作者签名，同一内容换个键序或空白就会得到不同的消息标识，攻击者可借此制造重复条目、延展消息或分叉自己的日志。给TypeScript日志摄入库写供复制调用的消息校验包，验签由调用方注入，以下按本题规定：收到的消息文本必须等于其解析后按固定键序（previous、sequence、author、timestamp、hash、content、signature）、两空格缩进重新序列化的结果，不等拒绝；消息 id 为含 signature 的规范文本 UTF-8 字节的 SHA-256 Base64 加 .sha256 后缀；previous 须等于上一条的 id，sequence 恰好加一，author 须等于该日志的主人；同一 sequence 出现不同 id 视为分叉，该作者日志之后的消息一律不收，并保存两条作证据。用换键序、改空白、断链、跳号和分叉验证。
