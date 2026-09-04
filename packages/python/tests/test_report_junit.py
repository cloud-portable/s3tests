import io
import unittest
from datetime import datetime

from cloud_portable_s3tests import CheckFailure, StepResult, VectorResult
from cloud_portable_s3tests.report import Meta, junit


def sample_results():
    return [
        VectorResult(id="multipart-0001", group="multipart", title="two-part upload", tags=["tier-1", "multipart"],
                     outcome="pass", duration=1234_000_000),
        VectorResult(id="multipart-0007", group="multipart", outcome="fail", steps=[
            StepResult(index=0, name="CreateMultipartUpload", passed=True),
            StepResult(index=2, name="CompleteMultipartUpload", failures=[
                CheckFailure("status", "400", "200"),
                CheckFailure("error", 'InvalidPart & "<quoted>"', "(no error)"),
            ]),
        ]),
        VectorResult(id="lifecycle-config-0010", group="lifecycle-config", outcome="fail",
                     runner_error="operation PutBucketLifecycle is not supported",
                     steps=[StepResult(index=1, name="PutBucketLifecycle", err="operation PutBucketLifecycle is not supported")]),
        VectorResult(id="versioning-0003", group="versioning", outcome="blocked", reason="prerequisite $bucket b1: outage"),
        VectorResult(id="versioning-0004", group="versioning", outcome="skipped", reason="excluded by tag filter"),
        VectorResult(id="object-crud-0169", group="object-crud", outcome="pass", warnings=["teardown x: BucketNotEmpty"]),
    ]


def render(results, meta) -> str:
    buf = io.BytesIO()
    junit.write(buf, results, meta)
    return buf.getvalue().decode()


class TestReportJunit(unittest.TestCase):
    def test_mapping_per_the_reporting_guide(self):
        out = render(sample_results(), Meta(corpus_version="1.0.0", target="MinIO TEST", properties={"zeta": "z", "alpha": "a"}))
        for want in [
            '<testsuites name="s3vectors" tests="6" failures="1" errors="1" skipped="2"',
            '<testsuite name="multipart" tests="2" failures="1" errors="0" skipped="0"',
            '<property name="corpusVersion" value="1.0.0"></property>',
            '<property name="target" value="MinIO TEST"></property>',
            '<testcase name="multipart-0001" classname="multipart" time="1.234">',
            '<property name="tags" value="tier-1,multipart"></property>',
            '<failure message="step 3 (CompleteMultipartUpload): status: expected 400, got 200">',
            'error: expected InvalidPart &amp; "&lt;quoted&gt;", got (no error)',
            '<error message="runner error: operation PutBucketLifecycle is not supported">',
            '<skipped message="blocked: prerequisite $bucket b1: outage"></skipped>',
            '<skipped message="skipped: excluded by tag filter"></skipped>',
            "<system-out>teardown x: BucketNotEmpty</system-out>",
        ]:
            self.assertIn(want, out)
        # Properties sorted; blocked never a failure.
        self.assertLess(out.index("alpha"), out.index("zeta"))
        self.assertNotIn('<failure message="blocked', out)

    def test_timestamp_appears_only_when_generated_at_set_utc_iso_8601(self):
        when = datetime.fromisoformat("2026-09-03T14:05:06+02:00")
        out = render(sample_results(), Meta(generated_at=when))
        self.assertIn('timestamp="2026-09-03T12:05:06"', out)
        self.assertNotIn("timestamp=", render(sample_results(), Meta()))

    def test_omit_skipped(self):
        out = render(sample_results(), Meta(omit_skipped=True))
        self.assertNotIn("versioning-0004", out)
        self.assertIn("versioning-0003", out, "blocked kept")
        self.assertIn('tests="5"', out)

    def test_deterministic_and_valid_for_an_empty_run(self):
        meta = Meta(corpus_version="1.0.0", properties={"b": "2", "a": "1"})
        self.assertEqual(render(sample_results(), meta), render(sample_results(), meta))
        empty = render([], Meta())
        self.assertTrue(empty.startswith('<?xml version="1.0" encoding="UTF-8"?>'))
        self.assertIn('tests="0"', empty)


if __name__ == "__main__":
    unittest.main()
