import unittest

from transducers import NotAccepted, RejectedInput, Transducer

from .helpers import dedup, expander, terminator


class TestValidation(unittest.TestCase):
    def test_initial_state_must_exist(self):
        with self.assertRaises(ValueError):
            Transducer(
                states={"q"}, alphabet_in={"a"}, alphabet_out={"a"},
                initial="nope", finals={"q"}, transitions={},
            )

    def test_final_states_must_exist(self):
        with self.assertRaises(ValueError):
            Transducer(
                states={"q"}, alphabet_in={"a"}, alphabet_out={"a"},
                initial="q", finals={"zzz"}, transitions={},
            )

    def test_transition_symbols_checked(self):
        with self.assertRaises(ValueError):
            Transducer(
                states={"q"}, alphabet_in={"a"}, alphabet_out={"a"},
                initial="q", finals={"q"},
                transitions={("q", "b"): ("q", ("a",))},  # 'b' not in alphabet_in
            )
        with self.assertRaises(ValueError):
            Transducer(
                states={"q"}, alphabet_in={"a"}, alphabet_out={"a"},
                initial="q", finals={"q"},
                transitions={("q", "a"): ("q", ("z",))},  # 'z' not in alphabet_out
            )

    def test_final_outputs_checked(self):
        with self.assertRaises(ValueError):
            Transducer(
                states={"q"}, alphabet_in={"a"}, alphabet_out={"a"},
                initial="q", finals={"q"}, transitions={},
                final_outputs={"ghost": ("a",)},
            )


class TestStep(unittest.TestCase):
    def test_epsilon_output(self):
        t = dedup()
        state, outs = t.step("seen:a", "a")
        self.assertEqual(outs, ())  # epsilon: duplicate dropped
        self.assertEqual(state, "seen:a")

    def test_one_to_many(self):
        t = expander()
        _, outs = t.step("q", "a")
        self.assertEqual(outs, ("a", "x"))
        _, outs = t.step("q", "c")
        self.assertEqual(outs, ())

    def test_rejected_input(self):
        t = expander()
        with self.assertRaises(RejectedInput):
            t.step("q", "not-a-symbol")

    def test_max_outputs_per_step(self):
        self.assertEqual(expander().max_outputs_per_step(), 2)
        self.assertEqual(terminator().max_outputs_per_step(), 1)


class TestEndOfStream(unittest.TestCase):
    def test_final_output_and_acceptance(self):
        t = terminator()
        self.assertEqual(t.final_output("q"), ("END",))
        self.assertTrue(t.accepts("q"))

    def test_non_accepting_state(self):
        t = Transducer(
            states={"even", "odd"}, alphabet_in={"a"}, alphabet_out={"a"},
            initial="even", finals={"even"},
            transitions={
                ("even", "a"): ("odd", ("a",)),
                ("odd", "a"): ("even", ("a",)),
            },
        )
        self.assertFalse(t.accepts("odd"))


if __name__ == "__main__":
    unittest.main()
