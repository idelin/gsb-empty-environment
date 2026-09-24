"""Test suite for minivm. Run: python3.11 -m unittest discover -s tests -t . -v"""

import gc
import json
import unittest
import weakref

from minivm import (
    DONE,
    CompileError,
    GuestUncaught,
    HeapQuotaError,
    Machine,
    SnapshotError,
    StateError,
    StepLimitError,
    compile_program,
    snapshot,
    tokenize,
)


def run_to_done(co, sends=None, max_steps=1_000_000):
    """Resume a coroutine until done; feed `sends` to successive yields."""
    sends = list(sends or [])
    yields = []
    value = None
    while True:
        out = co.resume(value, max_steps=max_steps)
        if out is DONE:
            return co.result, yields
        yields.append(out)
        value = sends.pop(0) if sends else None


def make(src, **machine_kw):
    machine = Machine(compile_program(src), **machine_kw)
    return machine, machine.start()


def evaluate(src, **machine_kw):
    machine, co = make(src, **machine_kw)
    result, _ = run_to_done(co)
    assert co.status == "done"
    return result, machine


COUNTER_SRC = """
(def make-counter ()
  (let n 0)
  (list (fn () (set n (+ n 1)) n)
        (fn () n)))
(let c (make-counter))
(let inc (list-get c 0))
(let get (list-get c 1))
"""


class TestClosures(unittest.TestCase):
    def test_shared_counter(self):
        src = COUNTER_SRC + "(inc) (inc) (get)"
        result, _ = evaluate(src)
        self.assertEqual(result, 2)

    def test_alias_mutation_shared_box(self):
        # Two closures aliasing the same mutable box: writes through one
        # alias are visible through the other.
        src = """
        (let box (list 0))
        (let reader (fn () (list-get box 0)))
        (let writer (fn (v) (list-set! box 0 v)))
        (writer 42)
        (reader)
        """
        result, _ = evaluate(src)
        self.assertEqual(result, 42)

    def test_alias_mutation_after_restore(self):
        src = """
        (let box (list 0))
        (let reader (fn () (list-get box 0)))
        (let writer (fn (v) (list-set! box 0 v)))
        (writer 7)
        (yield (reader))
        (writer 99)
        (reader)
        """
        prog = compile_program(src)
        machine, co = make(src)
        self.assertEqual(co.resume(), 7)
        data = snapshot.dumps(co)
        co2 = snapshot.restore(Machine(prog), data)
        result, _ = run_to_done(co2)
        self.assertEqual(result, 99)

    def test_loop_bindings_independent(self):
        # each-iteration fresh binding: closures do not share the loop var.
        src = """
        (let fns (list))
        (each x (list 10 20 30) (push fns (fn () x)))
        (list ((list-get fns 0)) ((list-get fns 1)) ((list-get fns 2)))
        """
        result, _ = evaluate(src)
        self.assertEqual(result, [10, 20, 30])

    def test_while_let_bindings_independent(self):
        src = """
        (let fns (list))
        (let i 0)
        (while (< i 3)
          (let v (* i 100))
          (push fns (fn () v))
          (set i (+ i 1)))
        (list ((list-get fns 0)) ((list-get fns 1)) ((list-get fns 2)))
        """
        result, _ = evaluate(src)
        self.assertEqual(result, [0, 100, 200])


class TestRecursion(unittest.TestCase):
    def test_factorial(self):
        src = "(def fact (n) (if (== n 0) 1 (* n (fact (- n 1))))) (fact 10)"
        result, _ = evaluate(src)
        self.assertEqual(result, 3628800)

    def test_mutual_recursion(self):
        src = """
        (def even? (n) (if (== n 0) true (odd? (- n 1))))
        (def odd? (n) (if (== n 0) false (even? (- n 1))))
        (list (even? 10) (odd? 7))
        """
        result, _ = evaluate(src)
        self.assertEqual(result, [True, True])

    def test_deep_recursion(self):
        # 5000 deep non-tail recursion: the VM is iterative, no Python
        # recursion limit involved.
        src = "(def f (n) (if (== n 0) 0 (+ 1 (f (- n 1))))) (f 5000)"
        result, _ = evaluate(src)
        self.assertEqual(result, 5000)


class TestExceptions(unittest.TestCase):
    def test_finally_runs_on_normal_exit(self):
        src = """
        (let log (list))
        (try (push log "body") (finally (push log "fin")))
        log
        """
        result, _ = evaluate(src)
        self.assertEqual(result, ["body", "fin"])

    def test_finally_runs_on_throw_and_catch(self):
        src = """
        (let log (list))
        (try
          (try (throw "boom") (finally (push log "inner-fin")))
          (catch e (push log e))
          (finally (push log "outer-fin")))
        log
        """
        result, _ = evaluate(src)
        self.assertEqual(result, ["inner-fin", "boom", "outer-fin"])

    def test_uncaught_throw_kills_coroutine(self):
        machine, co = make('(throw "x")')
        with self.assertRaises(GuestUncaught):
            co.resume()
        self.assertEqual(co.status, "error")
        # No runnable coroutine is left behind.
        with self.assertRaises(StateError):
            co.resume()

    def test_throw_value_propagates(self):
        machine, co = make('(throw (list 1 2))')
        with self.assertRaises(GuestUncaught) as ctx:
            co.resume()
        self.assertEqual(ctx.exception.value, [1, 2])

    def test_catch_value(self):
        src = '(try (throw 41) (catch e (+ e 1)))'
        result, _ = evaluate(src)
        self.assertEqual(result, 42)


class TestCoroutines(unittest.TestCase):
    def test_yield_and_resume_values(self):
        src = """
        (let got (yield 1))
        (yield got)
        33
        """
        machine, co = make(src)
        self.assertEqual(co.resume(), 1)
        self.assertEqual(co.resume("sent-value"), "sent-value")
        self.assertEqual(co.resume(), DONE)
        self.assertEqual(co.result, 33)
        self.assertEqual(co.status, "done")

    def test_close_runs_nested_finally_exactly_once(self):
        events = []
        src = """
        (try
          (try (yield 1) (finally (emit "inner")))
          (finally (emit "outer")))
        (emit "unreachable")
        """
        machine = Machine(compile_program(src), globals={
            "emit": lambda m, x: events.append(x),
        })
        co = machine.start()
        self.assertEqual(co.resume(), 1)
        co.close()
        co.close()  # idempotent
        self.assertEqual(events, ["inner", "outer"])
        self.assertEqual(co.status, "closed")
        with self.assertRaises(StateError):
            co.resume()

    def test_close_after_snapshot_restore(self):
        events = []

        def build():
            return Machine(compile_program(SRC_NESTED_YIELD), globals={
                "emit": lambda m, x: events.append(x),
            })

        machine = build()
        co = machine.start()
        self.assertEqual(co.resume(), "suspended-in-try")
        data = snapshot.dumps(co)
        co2 = snapshot.restore(build(), data)
        co2.close()
        self.assertEqual(events, ["inner", "outer"])
        with self.assertRaises(StateError):
            co2.resume()

    def test_cancel_during_yield_in_function(self):
        events = []
        src = """
        (def worker ()
          (try
            (yield "working")
            (finally (emit "cleaned"))))
        (worker)
        """
        machine = Machine(compile_program(src), globals={
            "emit": lambda m, x: events.append(x),
        })
        co = machine.start()
        self.assertEqual(co.resume(), "working")
        co.close()
        self.assertEqual(events, ["cleaned"])
        self.assertEqual(co.status, "closed")


SRC_NESTED_YIELD = """
(try
  (try (yield "suspended-in-try") (finally (emit "inner")))
  (finally (emit "outer")))
(emit "after")
"""


class TestSnapshots(unittest.TestCase):
    def test_shared_counter_survives_restore(self):
        src = COUNTER_SRC + """
        (inc)
        (yield (get))
        (inc)
        (inc)
        (yield (get))
        (get)
        """
        prog = compile_program(src)
        machine = Machine(prog)
        co = machine.start()
        self.assertEqual(co.resume(), 1)
        data = snapshot.dumps(co)
        json.loads(data)  # must be plain JSON

        restored = snapshot.restore(Machine(prog), data)
        self.assertEqual(restored.resume(), 3)  # shared counter kept counting
        self.assertEqual(restored.resume(), DONE)
        self.assertEqual(restored.result, 3)

        # The original coroutine is unaffected by the restore.
        self.assertEqual(co.resume(), 3)

    def test_restore_rejects_changed_program(self):
        src = "(yield 1) 2"
        machine, co = make(src)
        co.resume()
        data = snapshot.dumps(co)
        other = Machine(compile_program("(yield 1) 3"))  # program changed
        with self.assertRaises(SnapshotError):
            snapshot.restore(other, data)

    def test_restore_rejects_version_mismatch(self):
        machine, co = make("(yield 1) 2")
        co.resume()
        data = snapshot.to_data(co)
        data["version"] = 999
        with self.assertRaises(SnapshotError):
            snapshot.restore(Machine(compile_program("(yield 1) 2")), data)

    def test_restore_rejects_garbage(self):
        machine = Machine(compile_program("1"))
        with self.assertRaises(SnapshotError):
            snapshot.restore(machine, {"nope": True})
        with self.assertRaises(SnapshotError):
            snapshot.restore(machine, "not json at all{")

    def test_host_function_not_serialized(self):
        machine, co = make("(let p print) (yield 1)")
        co.resume()
        with self.assertRaises(SnapshotError):
            snapshot.dumps(co)

    def test_snapshot_preserves_pending_finally(self):
        # Yield inside nested try: snapshot must keep both finally handlers.
        events = []

        def build():
            return Machine(compile_program(SRC_NESTED_YIELD), globals={
                "emit": lambda m, x: events.append(x),
            })

        co = build().start()
        co.resume()
        data = snapshot.dumps(co)
        # Resume the restored coroutine to completion: finallys run once.
        co2 = snapshot.restore(build(), data)
        run_to_done(co2)
        self.assertEqual(events, ["inner", "outer", "after"])

    def test_snapshot_inside_each_loop(self):
        src = """
        (let seen (list))
        (each x (list 1 2 3)
          (let got (yield x))
          (push seen got))
        seen
        """
        prog = compile_program(src)
        machine, co = None, Machine(prog).start()
        self.assertEqual(co.resume(), 1)
        data = snapshot.dumps(co)
        co2 = snapshot.restore(Machine(prog), data)
        self.assertEqual(co2.resume("a"), 2)
        self.assertEqual(co2.resume("b"), 3)
        self.assertEqual(co2.resume("c"), DONE)
        self.assertEqual(co2.result, ["a", "b", "c"])

    def test_cannot_snapshot_dead_coroutine(self):
        machine, co = make("1")
        co.resume()
        with self.assertRaises(SnapshotError):
            snapshot.dumps(co)


class TestLimits(unittest.TestCase):
    def test_step_limit(self):
        machine, co = make("(while true 0)")
        with self.assertRaises(StepLimitError):
            co.resume(max_steps=1000)
        self.assertEqual(co.status, "error")
        with self.assertRaises(StateError):
            co.resume()

    def test_heap_quota(self):
        src = """
        (let i 0)
        (let x 0)
        (while (< i 10000)
          (set x (list 1 2 3))
          (set i (+ i 1)))
        """
        machine, co = make(src, heap_quota=100)
        with self.assertRaises(HeapQuotaError):
            co.resume(max_steps=10_000_000)
        self.assertEqual(co.status, "error")

    def test_fault_runs_finally_cleanup(self):
        events = []
        src = """
        (try
          (while true 0)
          (finally (emit "cleaned")))
        """
        machine = Machine(compile_program(src), globals={
            "emit": lambda m, x: events.append(x),
        })
        co = machine.start()
        with self.assertRaises(StepLimitError):
            co.resume(max_steps=500)
        self.assertEqual(events, ["cleaned"])
        self.assertEqual(co.status, "error")


class TestGC(unittest.TestCase):
    def test_environment_cycle_collected(self):
        # (def f (fn () f)) creates closure -> env -> cell -> closure cycle.
        src = "(def f () f) (yield 1)"
        machine, co = make(src)
        co.resume()
        env = co.frames[0].env
        closure = env.cells[0]
        refs = [weakref.ref(env), weakref.ref(closure), weakref.ref(closure.env)]
        self.assertIs(closure.env.cells[0], closure)  # the cycle exists
        del machine, co, env, closure
        gc.collect()
        self.assertTrue(all(r() is None for r in refs))

    def test_coroutine_cycle_collected(self):
        src = "(def f (n) (if (== n 0) 0 (f (- n 1)))) (f 3)"
        machine, co = make(src)
        co.resume()
        refs = [weakref.ref(machine), weakref.ref(co)]
        del machine, co
        gc.collect()
        self.assertTrue(all(r() is None for r in refs))


class TestLexer(unittest.TestCase):
    def test_utf8_byte_spans(self):
        src = '(中 "héllo") x'
        tokens = tokenize(src)
        by_text = {t.text: t for t in tokens}
        # byte offsets must match the utf-8 encoding of the source
        encoded = src.encode("utf-8")
        for tok in tokens:
            if tok.kind in ("string",):
                continue
            self.assertEqual(encoded[tok.start:tok.end].decode("utf-8"), tok.text)
        # '中' is 3 utf-8 bytes: starts at byte 1, ends at byte 4
        self.assertEqual((by_text["中"].start, by_text["中"].end), (1, 4))
        # string span covers the quotes in bytes
        str_tok = [t for t in tokens if t.kind == "string"][0]
        self.assertEqual(encoded[str_tok.start:str_tok.end], '"héllo"'.encode("utf-8"))

    def test_ast_spans(self):
        from minivm.parser import parse

        nodes = parse("(+ 1 2)")
        self.assertEqual((nodes[0].start, nodes[0].end), (0, 7))


class TestCompileErrors(unittest.TestCase):
    def test_duplicate_binding(self):
        with self.assertRaises(CompileError):
            compile_program("(let x 1) (let x 2)")

    def test_bad_form(self):
        with self.assertRaises(CompileError):
            compile_program("(let 1 2)")


if __name__ == "__main__":
    unittest.main()
