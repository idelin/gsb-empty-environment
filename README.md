# SELinux转换链与neverallow

SELinux 策略允许域转换时，攻击者控制的低权限域可经多步 type_transition 与入口点链进入特权域。为Go策略评审组件实现供策略发布前调用的转换可达包：输入 allow 规则（源域、目标类型、类、权限集）、type_transition、域转换所需的 transition、entrypoint、execute 三条权限，以及 neverallow 规则；从指定起始域求经一次或多次 exec 可到达的域集合，对违反 neverallow（含 typeattribute 展开）的组合给出最短转换链作为证据；dontaudit 不影响放行。用多跳转换、缺 entrypoint 的断链、属性展开的 neverallow 和合法链验证。
