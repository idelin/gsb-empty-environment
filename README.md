# .netrc机名匹配

HTTP 客户端按 .netrc 为请求附带账号密码，攻击者通过重定向、近似域名或 default 条目，让凭据发到他的服务器。为Go的 HTTP 客户端写可导入的 RoundTripper 包：解析 netrc 记号（空白分隔，password 可用双引号包住含空白的值，macdef 到空行结束），machine 与请求主机比较时转小写、IDN 转 punycode、去掉末尾点，须整名相等，不做后缀匹配，端口不参与；default 条目只用于首个请求的原始主机；重定向到其他主机时，仅当该主机有 machine 条目才附带凭据，default 不适用；重定向回原主机也须重新匹配；请求已有 Authorization 头时不覆盖；明文 HTTP 是否附带由调用方开关决定。测近似域名、重定向到他站、default 泄漏、punycode。
