#!/usr/bin/env python3.11
"""Runnable usage sample for the build file access proxy.

Creates a throwaway root with `in/` (inputs) and `out/` (outputs), runs a
small "build" through the proxy, and prints every audit event as JSON.

Run from the repository root:  python3.11 examples/demo.py
"""

import json
import os
import shutil
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from buildproxy import FileProxy, ListSink, Policy, PolicyDenied


def main():
    tmp = tempfile.mkdtemp(prefix="proxy-demo-")
    try:
        root = os.path.join(tmp, "root")
        os.makedirs(os.path.join(root, "in"))
        os.makedirs(os.path.join(root, "out"))
        with open(os.path.join(root, "in", "main.c"), "w") as fh:
            fh.write("int main(void) { return 0; }\n")
        # a relative symlink inside the root is fine
        os.symlink("main.c", os.path.join(root, "in", "entry.c"))

        sink = ListSink()
        policy = Policy(readable=("in",), writable=("out",))

        with FileProxy(root, policy, audit_sink=sink) as proxy:
            source = proxy.read("in/entry.c")          # follows in-root link
            print("read in/entry.c ->", source.decode().strip())

            proxy.create("out/main.o", b"\x7fOBJ fake object")
            proxy.replace("out/main.o", b"\x7fOBJ recompiled")
            proxy.rename("out/main.o", "out/app.bin")
            proxy.replace("out/build.log", b"ok\n")
            # display only: out/ is write-only, so list via the host fs
            print("out/ ->", sorted(os.listdir(os.path.join(root, "out"))))

            proxy.unlink("out/build.log")

            try:
                proxy.read("out/app.bin")              # out/ is write-only
            except PolicyDenied as exc:
                print("denied as expected:", exc)

        print("\n-- audit log --")
        for ev in sink.events:
            print(json.dumps({
                "op": ev.op, "path": ev.path, "path2": ev.path2,
                "resolved": ev.resolved, "resolved2": ev.resolved2,
                "dev": ev.dev, "ino": ev.ino,
                "result": ev.result, "reason": ev.reason,
            }))
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    main()
