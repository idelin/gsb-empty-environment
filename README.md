# External Secrets命名空间与目标隔离

External Secrets Operator 按刷新间隔从外部密钥库同步成集群 Secret，攻击者可创建引用其他命名空间存储的 ExternalSecret 读取不属于自己的密钥，或让刷新写入已被他人拥有的 Secret。为 TypeScript 控制器写供每次调谐调用的隔离判定包：输入 ExternalSecret（命名空间、存储引用种类与名称、dataFrom 的 key 与 find.name 正则、目标 Secret 名与 creationPolicy：Owner、Orphan、Merge、None）、存储定义（命名空间范围或集群范围、命名空间白名单与标签选择器条件、允许的 key 前缀）与目标 Secret 现有属主；越出条件的命名空间拒绝；find.name 正则不得匹配 key 前缀之外的键；Owner 策略下目标 Secret 已有他人属主拒绝；Merge 不得覆盖带保护标签的键；刷新失败不得把旧值替换为空。用跨命名空间引用、正则越前缀、他人属主、Merge 覆盖和失败刷新验证。
