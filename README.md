# iOS文件保护类降级推导

应用把敏感文件设成锁屏后不可读的保护类，但原子写、复制和备份恢复会悄悄把文件换成默认保护类，设备在锁定状态下落入攻击者手里时，这些文件就能被读出。为TypeScript移动应用的静态审查服务写可导入的保护类推导包，输入对路径依次执行的操作序列（create 指定类或缺省、atomicWrite 指定类或缺省、copy、move、setAttributes、restore 备份恢复）和调用方给的路径规则（路径前缀要求的最低类）：类的强弱依次为 complete、completeUnlessOpen、completeUntilFirstUserAuthentication、none；缺省的 create 与缺省的 atomicWrite 取调用方给的应用默认类，且 atomicWrite 不继承被替换文件的原类；move 保留原类；copy 的副本取应用默认类，除非复制后紧跟 setAttributes；restore 一律取应用默认类；按序推导每个路径最终的类，路径落在多条规则下取最强要求，最终类弱于要求时报违规并指出造成降级的那一步。用 create complete 后缺省 atomicWrite、copy、restore 和 move 进受规则目录验证。
