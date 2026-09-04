import unittest

from cloud_portable_s3tests import CheckFailure, StepResult, VectorResult
from cloud_portable_s3tests.report import unittest as s3unittest


class Recorder:
    """The subset of TestCase that report() drives, recording the calls."""

    def __init__(self):
        self.logs = []
        self.events = []
        self.skipped = ""

    def log(self, msg):
        self.logs.append(msg)
        self.events.append("log")

    def skipTest(self, msg):
        self.skipped = msg
        self.events.append("skip")
        raise unittest.SkipTest(msg)

    def fail(self, msg):
        raise AssertionError(msg)


class TestReportUnittest(unittest.TestCase):
    def test_pass_logs_title_and_real_duration(self):
        r = Recorder()
        s3unittest.report(r, VectorResult(id="x", group="g", outcome="pass", title="put then get", duration=1234_000_000), log=r.log)
        self.assertEqual(r.logs, ["put then get (1.234s)"])
        self.assertEqual(r.skipped, "")

    def test_fail_raises_with_expected_actual_detail(self):
        r = Recorder()
        res = VectorResult(id="x", group="g", outcome="fail", steps=[StepResult(
            index=2, name="CompleteMultipartUpload", err="transport hiccup",
            failures=[CheckFailure("status", "400", "200"), CheckFailure("error", "InvalidPart", "(no error)")])])
        with self.assertRaises(AssertionError) as cm:
            s3unittest.report(r, res)
        self.assertEqual(str(cm.exception), "step 3 (CompleteMultipartUpload):\n  transport hiccup\n"
                                            "  status: expected 400, got 200\n  error: expected InvalidPart, got (no error)")

    def test_runner_error_raises_with_prefix(self):
        with self.assertRaisesRegex(RuntimeError, r"^runner error: operation X is not supported$"):
            s3unittest.report(Recorder(), VectorResult(id="x", group="g", outcome="fail", runner_error="operation X is not supported"))

    def test_blocked_and_skipped_skip_with_prefixed_reasons_warnings_log_first(self):
        r = Recorder()
        with self.assertRaises(unittest.SkipTest):
            s3unittest.report(r, VectorResult(id="x", group="g", outcome="blocked", reason="prerequisite $bucket b1: down"))
        self.assertEqual(r.skipped, "blocked: prerequisite $bucket b1: down")
        r = Recorder()
        with self.assertRaises(unittest.SkipTest):
            s3unittest.report(r, VectorResult(id="x", group="g", outcome="skipped", reason="excluded",
                                              warnings=["teardown x: leftover"]), log=r.log)
        self.assertEqual(r.skipped, "skipped: excluded")
        self.assertEqual(r.events, ["log", "skip"], "warnings must log before the terminal call")

    # Real unittest integration: pass/blocked/skipped results execute as named
    # subtests without failing the suite (the fail paths are recorder-only).
    def test_run_drives_real_subtests(self):
        s3unittest.run(self, [
            VectorResult(id="object-crud-0001", group="object-crud", outcome="pass", title="put then get", duration=42_000_000),
            VectorResult(id="versioning-0003", group="versioning", outcome="blocked", reason="no alt credential"),
            VectorResult(id="versioning-0004", group="versioning", outcome="skipped", reason="excluded"),
        ])


if __name__ == "__main__":
    unittest.main()
