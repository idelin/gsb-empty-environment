// 宽度子类型：含额外字段的记录可传给只读（@ro）少字段参数。
let get_x = fn (p: { x: Int } @ro) -> Int { p.x };
let point3 = { x = 1, y = 2, z = 3 };
get_x(point3);

// 可写（@rw）参数是不变的，下面调用若取消注释会在编译时被拒绝：
// let set_x = fn (p: { x: Int } @rw) -> { x: Int } { p with x = 0 };
// set_x(point3);   // error: @rw 不允许宽度子类型
