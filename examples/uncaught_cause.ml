// This program intentionally ends with an uncaught throw so you can see
// the cause chain printed by main.py: the finally block's throw overrides
// the original error, which is preserved as its cause.

fn cleanup() {
    throw "cleanup failed";
}

try {
    try {
        throw "original database error";
    } finally {
        cleanup();   // this throw overrides the original one
    }
} finally {
    print("outer finally still runs");
}
