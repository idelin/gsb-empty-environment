// finally semantics: always runs; its own control transfer overrides any
// pending one; a return value is computed BEFORE finally executes.

// 1. finally runs on the normal path and on the return path.
fn f() {
    try {
        return "returned from try";
    } finally {
        print("f: finally ran");
    }
}
print("f() ->", f());

// 2. A return inside finally OVERRIDES the pending return from try.
fn g() {
    try {
        return "from try";
    } finally {
        return "from finally";
    }
}
print("g() ->", g());

// 3. The return value is evaluated BEFORE finally runs: finally may change
//    the variable afterwards, but the already-computed value is returned.
let x = 1;
fn h() {
    try {
        return x;          // evaluated to 1 right here
    } finally {
        x = 99;            // mutates external state...
    }
}
print("h() ->", h());      // ...but the old value 1 is still returned
print("x is now", x);

// 4. A throw inside finally OVERRIDES a pending return.
fn k() {
    try {
        return "never seen";
    } finally {
        throw "finally failed";
    }
}
try {
    k();
} catch (err) {
    print("k() threw:", err);
}

// 5. Loop control from finally overrides a pending return as well.
fn find_first() {
    for (let i = 0; i < 3; i = i + 1) {
        try {
            if (i == 1) {
                return "returned from try";
            }
        } finally {
            if (i == 1) {
                break;       // swallows the pending return on iteration 1
            }
        }
    }
    return "fell out of the loop";
}
print("find_first() ->", find_first());

// 6. continue still runs finally, per iteration.
for (let i = 0; i < 3; i = i + 1) {
    try {
        if (i == 1) { continue; }
        print("body", i);
    } finally {
        print("finally", i);
    }
}
