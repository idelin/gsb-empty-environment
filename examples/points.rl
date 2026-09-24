// 记录、字段访问、更新（with）与显式扩展（extend）。
let origin = { x = 0, y = 0 };
let moved = origin with x = 10;          // 更新已存在字段
let p3 = extend moved with z = 7;        // 显式扩展新字段
p3;
p3.x + p3.y + p3.z;
origin;                                   // 原记录不受影响（纯语义）
