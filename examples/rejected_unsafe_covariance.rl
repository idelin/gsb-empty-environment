// 可写参数不能不安全协变：@rw 要求实参与形参类型精确一致。
let set_x = fn (p: { x: Int } @rw) -> { x: Int } { p with x = 0 };
set_x({ x = 1, y = 2 });
