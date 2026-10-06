"""Runnable usage sample:  python3.11 examples/demo.py"""

import os
import shutil
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from fsproxy import BuildFSProxy, Policy, ProxyError  # noqa: E402


def main():
    tmp = tempfile.mkdtemp(prefix="fsproxy-demo-")
    try:
        root = os.path.join(tmp, "root")
        os.makedirs(os.path.join(root, "in"))
        os.makedirs(os.path.join(root, "out"))
        with open(os.path.join(root, "in", "main.c"), "w") as f:
            f.write("int main(void){return 0;}\n")

        policy = Policy(readable=["in", "out"], writable=["out"])
        with BuildFSProxy(root, policy) as proxy:
            print("source:", proxy.read("in/main.c").decode().strip())
            proxy.create("out/main.o", b"\x7fELF...")          # compile output
            proxy.replace("out/main.o", b"\x7fELF...v2")       # atomic rebuild
            proxy.rename("out/main.o", "out/main.o.final")     # publish
            print("out dir:", proxy.list("out"))
            proxy.unlink("out/main.o.final")
            try:
                proxy.replace("in/main.c", b"malicious")       # input is read-only
            except ProxyError as exc:
                print("denied as expected:", exc)

            print("\naudit trail:")
            for ev in proxy.audit:
                objs = ",".join(f"{o.dev:x}:{o.ino}" for o in ev.objects) or "-"
                print(f"  {ev.op:7s} {ev.path:20s} -> {ev.resolved!s:18s} "
                      f"{ev.result:7s} {ev.reason or ''} [{objs}]")
    finally:
        shutil.rmtree(tmp, True)


if __name__ == "__main__":
    main()
