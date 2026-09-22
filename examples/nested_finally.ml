// Nested cleanup runs inside-out, and each finally gets its own chance to
// override the pending transfer.

fn layer(name) {
    print("cleanup:", name);
}

try {
    try {
        try {
            try {
                throw "boom";
            } finally {
                layer("inner");
            }
        } finally {
            layer("middle");
        }
    } finally {
        layer("outer");
    }
} catch (err) {
    print("caught afterwards:", err);
}
print("---");

// Inner finally overrides the throw with a return; the outer finally still
// runs afterwards and lets the (new) pending return continue.
fn deep() {
    try {
        try {
            throw "original";
        } finally {
            return "recovered by inner finally";
        }
    } finally {
        layer("outer of deep");
    }
}
print("deep() ->", deep());

// A throw in an inner finally overrides the original error but keeps it as
// the cause; the outer catch sees the NEW error.
try {
    try {
        throw "database error";
    } finally {
        throw "connection reset while cleaning up";
    }
} catch (err) {
    print("outer catch saw:", err);
}
