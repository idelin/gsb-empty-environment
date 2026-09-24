// 高阶函数与闭包。
let apply = fn (f: fn(Int @ro) -> Int @ro, x: Int @ro) -> Int { f(x) };
apply(fn (n: Int @ro) -> Int { n * 2 }, 21);
