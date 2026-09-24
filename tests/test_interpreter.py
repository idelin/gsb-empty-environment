import unittest

from recordlang.api import run_program


class InterpreterTest(unittest.TestCase):
    def test_arithmetic_and_if(self):
        self.assertEqual(run_program("1 + 2 * 3;"), ["7"])
        self.assertEqual(run_program("if 1 < 2 then 10 else 20;"), ["10"])

    def test_update_does_not_mutate_original(self):
        out = run_program("let r = { x = 1 };\nlet s = r with x = 2;\nr.x;\ns.x;")
        self.assertEqual(out, ["1", "2"])

    def test_extend(self):
        self.assertEqual(run_program("(extend { x = 1 } with y = 2).y;"), ["2"])

    def test_width_subtyping_at_run_time(self):
        src = """
        let get_x = fn (p: { x: Int } @ro) -> Int { p.x };
        get_x({ x = 1, y = 2, z = 3 });
        """
        self.assertEqual(run_program(src), ["1"])

    def test_closures(self):
        src = """
        let add = fn (x: Int @ro) -> fn(Int @ro) -> Int {
            fn (y: Int @ro) -> Int { x + y }
        };
        add(2)(3);
        """
        self.assertEqual(run_program(src), ["5"])

    def test_record_printing(self):
        self.assertEqual(run_program("{ x = 1, y = true };"), ["{x = 1, y = true}"])


if __name__ == "__main__":
    unittest.main()
