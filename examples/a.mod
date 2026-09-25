# Module a: imports a function from b, exports a function used by b.
import { isEven } from "b";

export fn isOdd(n) {
    if n == 0 { return false; }
    return isEven(n - 1);
}

print("init a");
