import { is_even } from "a"

export fn is_odd(n) {
  if n == 0 { return false }
  return is_even(n - 1)
}
