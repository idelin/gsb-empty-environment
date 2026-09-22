# 嵌套循环：内层 break 只跳出内层，外层 break 跳出外层
let sum = 0;
let i = 0;
while (i < 3) {
  let j = 0;
  while (j < 5) {
    if (j == 2) { break; }          # 只跳出 j 循环
    sum = sum + i * 10 + j;
    j = j + 1;
  }
  if (i == 1) { break; }            # 跳出 i 循环
  i = i + 1;
}
sum                                 # 表达式语句，结果被弹出
