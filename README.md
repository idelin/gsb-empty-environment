# AppArmor deny与执行转换

AppArmor 配置按路径规则放行，deny 规则优先于 allow，执行时按 ix、px、Px、ux 切换配置；攻击者借通配符、解释器与继承执行逃出受限配置。为Go加固评审组件实现供配置发布前调用的判定包：规则含 glob（* 不跨 /，** 跨 /）、权限 r、w、x、a 与 deny 标志，同一访问命中 deny 即拒绝，allow 取并集；执行类型 ix 继承当前配置，px 切换到命名配置且缺失则失败，Px 同 px 并清理环境，ux 无约束；受限配置里出现 ux、对 ** 的 w 权限或对 /proc/*/mem 的 w 就报告提权风险。用 deny 覆盖通配 allow、ux 规则、ix 继承的写权限和 /proc/self/mem 验证。
