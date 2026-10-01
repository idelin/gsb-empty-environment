"""可独立执行的反例 / 见证脚本测试：生成 → 写入临时目录 → 子进程独立运行。"""
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from cfg_analyzer import analyze
from cfg_analyzer.samples import SAMPLES
from cfg_analyzer.witness import emit_all

PYTHON = sys.executable  # 需为 Python 3.11+


class TestStandaloneWitnesses(unittest.TestCase):
    def run_script(self, name: str, text: str) -> str:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / name
            path.write_text(text, encoding="utf-8")
            proc = subprocess.run([PYTHON, str(path)], capture_output=True,
                                  text=True, timeout=60)
            self.assertEqual(proc.returncode, 0,
                             f"{name} 独立执行失败:\n{proc.stderr}")
            self.assertIn("OK", proc.stdout)
            return proc.stdout

    def test_all_samples_witnesses_execute(self):
        for name, (_desc, g) in SAMPLES.items():
            diag = analyze(g)
            for fname, text in emit_all(diag).items():
                with self.subTest(sample=name, script=fname):
                    self.run_script(f"{name}__{fname}", text)

    def test_ambiguity_counterexample_content(self):
        diag = analyze(SAMPLES["epsilon_context_cycle"][1])
        scripts = emit_all(diag)
        self.assertIn("counterexample_infinite_ambiguity.py", scripts)
        out = self.run_script("amb.py", scripts["counterexample_infinite_ambiguity.py"])
        self.assertIn("'a'", out)
        self.assertIn("无限", out)

    def test_empty_language_certificate_content(self):
        diag = analyze(SAMPLES["cycle_no_exit"][1])
        scripts = emit_all(diag)
        out = self.run_script("cert.py", scripts["check_generating_certificate.py"])
        self.assertIn("L(G) = ∅", out)

    def test_scripts_use_only_stdlib(self):
        for name, (_desc, g) in SAMPLES.items():
            diag = analyze(g)
            for fname, text in emit_all(diag).items():
                with self.subTest(sample=name, script=fname):
                    for line in text.splitlines():
                        if line.startswith(("import ", "from ")):
                            self.assertIn("json", line, f"非标准库依赖: {line}")


if __name__ == "__main__":
    unittest.main()
