// 演示程序: 定义到达与使用前未定义检查（注释含多字节 UTF-8 字符）
let a = 1;
let i = 0;
if a {
    b = 2;        // 只在 then 一支赋值
} else {
    a = 3;
}
print a;          // ok: a 在所有路径上均已定义
print b;          // warning: b 只在一支赋值，分支后不保证已定义
while i < 3 {
    c = i;        // 循环体内定义
    i = i + 1;
}
print c;          // warning: 循环可能执行零次，c 不保证已定义
print u;          // error: u 从未定义
halt;
print 99;         // info: 不可达语句
