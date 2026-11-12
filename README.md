# Postgres扩展安装闭包

低权限数据库用户想借 CREATE EXTENSION 的依赖链引入未受信扩展，或用路径技巧加载任意共享库。为Go数据库代理写可导入的扩展准入包，输入控制文件元数据与请求角色：非超级用户只能装 trusted=true 的扩展，且 requires 的传递闭包全部 trusted 并已安装；LOAD 与 C 函数的库名只许 $libdir/ 后无斜杠的基名，含 .. 或绝对路径拒绝；superuser 扩展对非超级用户拒绝。测依赖闭包、路径穿越。
