union Shape {
  Circle(int),
  Rectangle(int, int),
  Empty
}

let shape: Shape = Rectangle(3, 4)

match shape: Shape {
  Circle(r) | r < 0 -> "negative radius",
  Circle(r) -> "circle",
  Rectangle(w, h) | w == h -> "square",
  Rectangle(w, h) -> "rectangle",
  Empty -> "empty"
}
