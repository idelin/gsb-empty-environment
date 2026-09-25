// Reading an uninitialized constant across an import cycle is an error.
import { y } from "b"

export const x = y + 1
