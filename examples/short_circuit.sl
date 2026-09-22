# 短路求值：危险的右支不会执行
let a = false and (1 / 0 == 0);    # 不计算 1/0
let b = true or (1 / 0 == 0);      # 不计算 1/0
let c = 2 < 3 and 40 + 2;          # 取右值 42
let d = 1 > 9 or 7;                # 取右值 7
c + d
