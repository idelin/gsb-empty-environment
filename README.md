# PassRole加创建资源的提权路径

拥有 PassRole 和创建函数权限的主体，可以让代码以更高权限角色运行。为Go云策略分析包写可导入的提权路径求解：输入各主体与角色的策略（Allow 与 Deny、Action 与 Resource 通配、iam:PassedToService 条件）和角色信任策略，算出主体经 PassRole 加创建并调用函数、最多传递 3 级角色后能到达的角色集合；显式 Deny 优先；条件限定服务时只对该服务生效。用通配 Resource、Deny 覆盖、信任策略不含 lambda 和两级传递验证。
