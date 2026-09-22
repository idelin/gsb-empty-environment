let value: ((int, string), bool) = ((7, "ok"), true)

match value: ((int, string), bool) {
  ((n, text), flag) -> (n + 1, text, !flag)
}
