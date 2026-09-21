let apply = fun f. fun x. f x in
let twice = fun g. fun n. g (g n) in
let inc = fun n. n + 1 in
twice inc (apply inc 0)
