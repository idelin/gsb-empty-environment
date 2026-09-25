# Entry module. Imports a (which imports b, which imports a):
# the function cycle links fine because declarations are hoisted.
# b is imported twice (here and via a) but initialized only once.
import { isOdd } from "a";
import { isEven } from "b";

print(isOdd(7));
print(isEven(10));
