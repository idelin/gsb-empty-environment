# Lua沙箱全局对象图可达切断

嵌入 Lua 的网关用环境表限制用户脚本，攻击者仍可沿 string 元表、函数 upvalue、load 系列函数或 require 取回 debug、io 与 os。为Go宿主实现供沙箱构造器调用的环境可达性审查包：输入全局对象图（表、函数、元表、字段边、函数的 upvalue 边及内置标识），从沙箱环境表和 string、number 等共享元表出发求可达；若能到达 debug.*、io.*、os.execute、load、loadfile、dofile、require 或 string.dump 就报告最短路径。用元表 __index 回溯、upvalue 泄漏、白名单环境和无害数学库验证。
