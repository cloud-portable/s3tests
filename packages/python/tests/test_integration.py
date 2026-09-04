"""Integration test against a real S3 implementation (MinIO by default; see
the Makefile's `integration` target). It asserts runner *mechanics* — no
unexpected runner errors, sane outcome accounting, clean teardown — not the
target's compatibility score. Self-skips unless S3TESTS_ENDPOINT is set."""

import io
import os
import re
import sys
import unittest

from cloud_portable_s3tests import Config, Credential, Runner, apply_filters, groups, vectors
from cloud_portable_s3tests._runner import audit_client
from cloud_portable_s3tests.report import Meta, html, junit

ENDPOINT = os.environ.get("S3TESTS_ENDPOINT", "")

# Vectors whose failure is a known runner limitation, not a target problem.
ALLOWED_RUNNER_ERRORS: set[str] = set()


@unittest.skipUnless(ENDPOINT, "S3TESTS_ENDPOINT not set")
class TestIntegration(unittest.TestCase):
    def test_integration(self):
        credentials = Credential(os.environ.get("S3TESTS_ACCESS_KEY", "minioadmin"), os.environ.get("S3TESTS_SECRET_KEY", "minioadmin"))
        runner = Runner(Config(endpoint=ENDPOINT, credentials=credentials, concurrency=4))

        group_names = os.environ.get("S3TESTS_GROUPS", "object-crud,multipart,presigned,anon-access,checksums,wire-headers,cors").split(",")
        selected = apply_filters(vectors(), groups(*group_names))
        self.assertTrue(selected, "group filter selected nothing")

        counts = {"pass": 0, "fail": 0, "blocked": 0, "skipped": 0}
        collected = []
        for res in runner.run(selected):
            counts[res.outcome] += 1
            collected.append(res)
            if res.runner_error and res.id not in ALLOWED_RUNNER_ERRORS:
                self.fail(f"{res.id}: unexpected runner error: {res.runner_error}")
            for w in res.warnings:
                print(f"# {res.id}: warning: {w}", file=sys.stderr)
            # No credential provisioner is configured, so $credential vectors
            # must be blocked, and nothing else should be.
            if res.outcome == "blocked" and not re.search(r"\$credential|provision_credential", res.reason):
                self.fail(f"{res.id}: unexpected block: {res.reason}")
        total = len(collected)
        self.assertEqual(total, len(selected), "run must yield one result per selected vector")
        self.assertEqual(sum(counts.values()), total)
        self.assertGreater(counts["pass"], 0, "no vector passed — endpoint misconfigured?")
        print(f"# corpus {runner.corpus_version()} against {ENDPOINT}: pass={counts['pass']} fail={counts['fail']} "
              f"blocked={counts['blocked']}", file=sys.stderr)

        # Both reporters must render the real results.
        meta = Meta(corpus_version=runner.corpus_version(), target=ENDPOINT)
        buf = io.BytesIO()
        junit.write(buf, collected, meta)
        self.assertIn(f'tests="{total}"', buf.getvalue().decode())
        buf = io.BytesIO()
        html.write(buf, collected, meta)
        page = buf.getvalue().decode()
        self.assertTrue(len(page) > 10_000 and 'id="group-multipart"' in page)
        self.assertNotIn("<script", page, "html report must contain no JavaScript")

        # Teardown audit: no runner buckets may survive (the curated groups
        # contain no COMPLIANCE-retention vectors). "s3tests-" is the
        # documented default bucket_prefix.
        audit = audit_client(Config(endpoint=ENDPOINT, credentials=credentials))
        leaked = [b["Name"] for b in audit.list_buckets().get("Buckets", []) if b["Name"].startswith("s3tests-")]
        self.assertEqual(leaked, [], "teardown leaked buckets")


if __name__ == "__main__":
    unittest.main()
