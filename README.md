# HTTP消息签名覆盖组件

攻击者改动未被覆盖的请求部分，或在头值里塞换行伪造签名基。为Go网关写可导入的 RFC 9421 请求签名校验包：alg 须来自密钥登记而非消息；必须覆盖 @method、@target-uri、content-digest（正文 SHA-256 重算相符）与 created，created 偏差不超60秒，expires 已过拒绝；构造签名基时头值含 CR 或 LF 即拒，重复头按字段顺序合并。测漏覆盖、换 alg、正文改动、换行注入。
