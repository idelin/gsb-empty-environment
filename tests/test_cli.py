import io
import json
import unittest
from contextlib import redirect_stdout
from pathlib import Path

from abicompat.cli import main

ROOT = Path(__file__).resolve().parent.parent
PACKAGES = str(ROOT / "examples" / "packages.json")
BUILD = str(ROOT / "examples" / "build.json")


class CliTest(unittest.TestCase):
    def test_json_report_matches_expected_sample(self):
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            code = main(["--packages", PACKAGES, "--build", BUILD, "--format", "json"])
        self.assertEqual(code, 0)
        payload = json.loads(buffer.getvalue())
        roots = sorted({finding["root"] for finding in payload["findings"]})
        self.assertEqual(roots, ["app", "cli"])

        app_findings = [f for f in payload["findings"] if f["root"] == "app"]
        parse_findings = [
            f for f in app_findings if f["symbol"] == "parse"
        ]
        reasons = sorted(f["reason"] for f in parse_findings)
        self.assertEqual(
            reasons,
            ["marker-removed", "param-became-required", "return-type-widened"],
        )
        for finding in parse_findings:
            chains = finding["path_chains"]
            self.assertIn(
                "app.main -> libhttp.fetch -> libcore.parse", chains
            )
            self.assertIn(
                "app.handle_body -> libhttp.decode_body -> libcore.parse",
                chains,
            )

        cli_findings = [f for f in payload["findings"] if f["root"] == "cli"]
        self.assertEqual(len(cli_findings), 1)
        self.assertEqual(cli_findings[0]["reason"], "param-type-narrowed")
        self.assertEqual(
            cli_findings[0]["affected_callsites"][0]["file"],
            "src/cli/run.py",
        )

        unreached = {entry["symbol"] for entry in payload["unreached_changes"]}
        self.assertIn("uncalled_legacy", unreached)
        self.assertIn("encode", unreached)
        self.assertIn("STATUS", unreached)
        self.assertNotIn("_secret", unreached)

        by_package = {node["package"]: node for node in payload["nodes"]}
        self.assertEqual(by_package["libcore"]["bump"], "major")
        self.assertEqual(by_package["libhttp"]["bump"], "minor")
        self.assertEqual(by_package["libmath"]["bump"], "major")
        self.assertEqual(by_package["app"]["bump"], "none")

    def test_text_report_and_check_flag(self):
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            code = main(
                [
                    "--packages",
                    PACKAGES,
                    "--build",
                    BUILD,
                    "--hide-unreached",
                    "--check",
                ]
            )
        self.assertEqual(code, 1)
        text = buffer.getvalue()
        self.assertIn("ROOT app", text)
        self.assertIn("app.main -> libhttp.fetch -> libcore.parse", text)
        self.assertNotIn("Not impacting any root", text)


if __name__ == "__main__":
    unittest.main()
