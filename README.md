# 特权程序库搜索路径

带 setuid 的辅助程序依赖共享库，若搜索路径经过普通用户可写目录，攻击者可放入同名库并获得特权。写供 Go 发布准入服务调用的 ELF 搜索路径判定包：用 debug/elf 读取 DT_NEEDED、RPATH、RUNPATH 与 $ORIGIN，结合调用方提供的 ld.so.cache 条目、默认目录和文件属主权限视图，按动态加载器顺序判断每个依赖会落到哪个目录；/etc/ld.so.preload 中的条目同样判断。用可写 RPATH、相对路径、$ORIGIN 指向可写目录和安全布局验证。
