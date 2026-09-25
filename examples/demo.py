"""Runnable demo: interleaved coroutines, close/finally, terminal state.

Run from the repo root:
    python3.11 examples/demo.py
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from minicoro import Interpreter, LangError  # noqa: E402

SOURCE = """
fn producer() {
    let i = 1;
    while i <= 3 {
        let got = yield i * 10;
        print("producer resumed with", got);
        i = i + 1;
    }
    return "producer-done";
}

fn consumer() {
    try {
        let n = yield "ready";
        print("consumer got", n);
        yield "never-yielded";
    } finally {
        print("consumer finally: cleanup ran");
    }
}

fn boom() {
    try {
        let x = 1 / 0;
    } finally {
        print("boom finally: cleanup ran");
    }
}
"""


def show(label, result):
    print("%-28s -> status=%-9s value=%r terminal=%s"
          % (label, result.status, result.value, result.terminal))


def main():
    interp = Interpreter()
    interp.load(SOURCE)

    print("== interleave two coroutines (locals stay separate) ==")
    producer = interp.spawn("producer")
    consumer = interp.spawn("consumer")
    show("producer.resume()", producer.resume())
    show("consumer.resume()", consumer.resume())
    show("producer.resume(7)", producer.resume(7))
    show("consumer.resume(99)", consumer.resume(99))

    print("== close a suspended coroutine: finally runs once ==")
    show("consumer.close()", consumer.close())
    show("consumer.close() again", consumer.close())
    show("consumer.resume() after close", consumer.resume())

    print("== run producer to completion, then resume again ==")
    show("producer.resume(8)", producer.resume(8))
    show("producer.resume(9)", producer.resume(9))
    show("producer.resume() terminal", producer.resume())

    print("== exception unwinds the cleanup stack ==")
    boom = interp.spawn("boom")
    try:
        boom.resume()
    except LangError as err:
        print("boom raised:", err)
    show("boom.resume() after failure", boom.resume())

    print("== captured print output ==")
    for line in interp.output:
        print("  |", line)


if __name__ == "__main__":
    main()
