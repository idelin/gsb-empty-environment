# GraalVM宿主类查找放行规则

GraalVM 多语言上下文里，来宾脚本经 Java.type 按名字查宿主类，攻击者用内部类的 $ 名、数组类名或同前缀的另一个包绕过放行规则，取到 Runtime 与 ProcessBuilder。为 TypeScript 配置服务写供上下文创建前调用的权限校验包：输入上下文选项与宿主类放行清单，allowAllAccess 或 allowNativeAccess 开启、宿主访问策略选了全部、允许创建线程且未设资源上限一律拒绝；类名按 Java 二进制名匹配，放行项 com.acme.api. 只匹配该包及子包，com.acme.api2 与 com.acme.apix 不匹配，含 $ 的内部类继承外部类的放行，数组类 [Lcom.acme.api.Foo; 看元素类型；java.lang.reflect、java.lang.invoke 与 sun. 开头的包永不放行。用同前缀包、内部类、数组类、全放开选项和合规配置验证。
