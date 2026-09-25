# Module b: imports a function from a, exports a function used by a.
import { isOdd } from "a";

export fn isEven(n) {
    if n == 0 { return true; }
    return isOdd(n - 1);
}

print("init b");
