import io
import os
import tempfile
import unittest
from pathlib import Path

from cloud_portable_s3tests import vectors
from cloud_portable_s3tests._cli import run
from helpers.canned import fail500


def pick_vector(want):
    v = next((v for v in vectors() if want(v)), None)
    assert v is not None, "no matching vector in corpus"
    return v["id"]


class TestCli(unittest.TestCase):
    def setUp(self):
        self.srv = fail500()
        self.addCleanup(self.srv.close)
        self.base = ["--endpoint", self.srv.url, "--access-key", "AK", "--secret-key", "SK"]

    def test_failures_exit_1_and_write_every_requested_report(self):
        vid = pick_vector(lambda v: not v.get("prerequisites"))
        d = Path(tempfile.mkdtemp(prefix="s3tests-cli-"))
        junit_path, html_path = d / "report.xml", d / "report.html"
        out, err = io.StringIO(), io.StringIO()
        code = run(self.base + ["--ids", vid, "-r", f"junit={junit_path}", "--report", f"html={html_path}"], out, err)
        self.assertEqual(code, 1, out.getvalue() + err.getvalue())
        for want in [vid, "1 fail", "wrote junit report", "wrote html report"]:
            self.assertIn(want, out.getvalue())
        for p in (junit_path, html_path):
            self.assertIn(vid, p.read_text("utf-8"), f"{p} must mention {vid}")

    def test_blocked_only_runs_exit_0_quiet_suppresses_progress(self):
        vid = pick_vector(lambda v: (v.get("prerequisites") or [{}])[0].get("$bucket"))
        out = io.StringIO()
        code = run(self.base + ["--ids", vid, "--quiet"], out, io.StringIO())
        self.assertEqual(code, 0, out.getvalue())
        self.assertIn("1 blocked", out.getvalue())
        self.assertNotIn("blocked " + vid, out.getvalue(), "--quiet must suppress progress lines")

    def test_skip_ids_records_the_vector_as_skipped_and_exits_0(self):
        vid = pick_vector(lambda v: not v.get("prerequisites"))
        junit_path = Path(tempfile.mkdtemp(prefix="s3tests-cli-")) / "report.xml"
        out, err = io.StringIO(), io.StringIO()
        code = run(self.base + ["--ids", vid, "--skip-ids", vid, "-r", f"junit={junit_path}"], out, err)
        self.assertEqual(code, 0, out.getvalue() + err.getvalue())
        for want in ["skipped " + vid, "skipped by --skip-ids", "1 vectors: 0 pass, 0 fail (0 runner errors), 0 blocked, 1 skipped"]:
            self.assertIn(want, out.getvalue())
        xml = junit_path.read_text("utf-8")
        for want in [vid, "skipped: skipped by --skip-ids", 'name="skip-ids"']:
            self.assertIn(want, xml)

    def test_usage_errors_exit_2(self):
        err = io.StringIO()
        self.assertEqual(run(["--access-key", "AK"], io.StringIO(), err), 2)
        self.assertIn("--endpoint", err.getvalue())
        self.assertEqual(run(["--endpoint", "http://x", "--access-key", "AK", "--secret-key", "SK", "--ids", "no-such-0001"], io.StringIO(), io.StringIO()), 2)
        err = io.StringIO()
        self.assertEqual(run(["--bogus"], io.StringIO(), err), 2)
        self.assertIn("error: ", err.getvalue())
        self.assertIn("usage: s3tests", err.getvalue())
        out = io.StringIO()
        self.assertEqual(run(["-h"], out, io.StringIO()), 0)
        self.assertTrue(out.getvalue().startswith("usage: s3tests"))

    def test_report_validation(self):
        base = ["--endpoint", "http://x", "--access-key", "AK", "--secret-key", "SK"]
        for bad in ["tap=report.tap", "tap", "junit=", "=x"]:
            self.assertEqual(run(base + ["-r", bad], io.StringIO(), io.StringIO()), 2, bad)
        err = io.StringIO()
        run(base + ["-r", "tap=x"], io.StringIO(), err)
        self.assertIn("formats: html, junit", err.getvalue())

    def test_bare_format_names_use_default_paths_in_the_working_directory(self):
        vid = pick_vector(lambda v: not v.get("prerequisites"))
        d = tempfile.mkdtemp(prefix="s3tests-cli-")
        prev = os.getcwd()
        os.chdir(d)
        try:
            out = io.StringIO()
            run(self.base + ["--ids", vid, "--quiet", "-r", "junit", "-r", "html"], out, io.StringIO())
            for p in ("report.xml", "report.html"):
                self.assertTrue((Path(d) / p).exists(), f"{p} missing")
                self.assertIn("report " + p, out.getvalue())
        finally:
            os.chdir(prev)


if __name__ == "__main__":
    unittest.main()
