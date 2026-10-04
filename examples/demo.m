#define one() = 1 #end
#define twice(x) = x + x #end
#define wrap(x) locals(tmp) = tmp = x ; tmp #end
twice(one())
wrap(result)
