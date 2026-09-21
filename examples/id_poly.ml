// The identity function is generalized in a let binding and reused
// at two distinct types: Int -> Int and Bool -> Bool.
let id = fun x. x in
let ignored = id true in
id 40 + 2
