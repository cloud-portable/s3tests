import copy
import re
import threading
import unittest

from cloud_portable_s3tests import (
    Config, Credential, Runner, apply_filters, exclude_groups, exclude_ids, exclude_tags, groups, ids, skip, tags, vectors,
)
from helpers.fake_s3 import FakeS3


def pass_vector():
    return {
        "id": "test-0001",
        "group": "test",
        "kind": "api",
        "title": "get seeded object",
        "tags": ["tier-1", "test"],
        "prerequisites": [
            {"$bucket": {"handle": "b1"}},
            {"$object": {"handle": "o1", "bucket": "b1", "key": "hello.txt", "body": "hello world"}},
        ],
        "data": {"pat": {"$pattern": {"pattern": "hello world", "size": 11}}},
        "steps": [
            {"$operation": {
                "name": "GetObject",
                "params": {"Bucket": "${res.b1.name}", "Key": "${res.o1.key}"},
                "capture": {"etag": "ETag"},
                "expect": {
                    "status": 200,
                    "response": {"ContentLength": 11, "ETag": "${data.pat.etag}"},
                    "headers": {"etag": {"$exists": True}},
                    "body": {"$data": "pat"},
                },
            }},
            {"$operation": {
                "name": "HeadObject",
                "params": {"Bucket": "${res.b1.name}", "Key": "${res.o1.key}"},
                "expect": {"response": {"ETag": "${cap.etag}"}},
            }},
            {"$operation": {
                "name": "GetObject",
                "params": {"Bucket": "${res.b1.name}", "Key": "missing.bin"},
                "expect": {"status": 404, "error": "NoSuchKey"},
            }},
        ],
    }


def new_runner(url, **extra):
    return Runner(Config(endpoint=url, credentials=Credential("AK", "SK"), **extra))


def run_one(runner, vector, **kw):
    for res in runner.run([vector], **kw):
        return res
    raise AssertionError("no result yielded")


class FailingProvisioner:
    def bucket(self, target, prereq, name, cancel=None):
        raise RuntimeError("simulated provisioning outage")

    def object(self, target, prereq, bucket_name, body, cancel=None):
        raise RuntimeError("unreachable")

    def teardown(self, target, buckets, cancel=None):
        return []


class TestRunner(unittest.TestCase):
    def setUp(self):
        self.srv = FakeS3()
        self.addCleanup(self.srv.close)

    def test_pass_vector_prereqs_captures_expectations_teardown(self):
        res = run_one(new_runner(self.srv.url), pass_vector())
        self.assertEqual(res.outcome, "pass", res)
        self.assertEqual(len(res.steps), 3)
        self.assertTrue(res.steps[0].captured["etag"])
        self.assertEqual(res.warnings, [])
        self.assertEqual(self.srv.buckets, {}, "teardown must remove the bucket")

    def test_fail_vector_aborts_at_the_failing_step_without_runner_error(self):
        v = pass_vector()
        v["steps"][0]["$operation"]["expect"]["response"] = {"ContentLength": 999}
        res = run_one(new_runner(self.srv.url), v)
        self.assertEqual(res.outcome, "fail")
        self.assertEqual(res.runner_error, "")
        self.assertEqual(len(res.steps), 1, "failing step must abort the vector")
        self.assertEqual(len(res.steps[0].failures), 1)
        self.assertEqual(res.steps[0].failures[0].field, "response.ContentLength")

    def test_prerequisite_failure_blocks_steps_never_run(self):
        res = run_one(new_runner(self.srv.url, provisioner=FailingProvisioner()), pass_vector())
        self.assertEqual(res.outcome, "blocked")
        self.assertRegex(res.reason, r"prerequisite \$bucket b1")
        self.assertEqual(res.steps, [])

    def test_credential_without_provision_credential_blocks(self):
        res = run_one(new_runner(self.srv.url), {
            "id": "test-0002", "group": "test", "kind": "api", "tags": ["tier-1"],
            "prerequisites": [{"$credential": {"handle": "alt"}}],
            "steps": [{"$operation": {"name": "ListBuckets", "identity": "alt"}}],
        })
        self.assertEqual(res.outcome, "blocked")
        self.assertIn("provision_credential", res.reason)

    def test_unresolvable_placeholder_is_a_runner_error(self):
        v = pass_vector()
        v["steps"][0]["$operation"]["params"]["Key"] = "${cap.neverCaptured}"
        res = run_one(new_runner(self.srv.url), v)
        self.assertEqual(res.outcome, "fail")
        self.assertRegex(res.runner_error, r"unresolvable placeholder")

    def test_vectors_loads_the_api_corpus_with_groups(self):
        all_ = vectors()
        self.assertGreater(len(all_), 1000)
        for v in all_:
            self.assertNotEqual(v["group"], "signing")
            self.assertEqual(v["kind"], "api")
            self.assertTrue(v["group"] and v["id"])

    def test_apply_filters_composes_with_and_semantics(self):
        all_ = vectors()
        self.assertEqual(len(apply_filters(all_)), len(all_))
        one = apply_filters(all_, ids("object-crud-0001"))
        self.assertEqual([v["id"] for v in one], ["object-crud-0001"])
        mp = apply_filters(all_, groups("multipart"))
        self.assertTrue(mp and all(v["group"] == "multipart" for v in mp))
        tier1mp = apply_filters(all_, groups("multipart"), tags("tier-1"))
        self.assertTrue(0 < len(tier1mp) < len(mp))
        self.assertEqual(len(apply_filters(mp, exclude_ids(mp[0]["id"]))), len(mp) - 1)
        self.assertEqual(apply_filters(mp, exclude_groups("multipart")), [])
        self.assertEqual(apply_filters(tier1mp, exclude_tags("tier-1")), [])
        custom = apply_filters(all_, lambda v: len(v.get("steps") or []) > 10)
        self.assertTrue(all(len(v["steps"]) > 10 for v in custom))

    def test_run_yields_exactly_the_given_vectors_breaking_cancels(self):
        runner = new_runner(self.srv.url, concurrency=2)
        lst = [pass_vector(), {**pass_vector(), "id": "test-0003"}, {**pass_vector(), "id": "test-0004"}]
        seen = 0
        for res in runner.run(lst):
            self.assertNotEqual(res.outcome, "skipped", "run must not skip vectors without a skip rule")
            seen += 1
            break  # must cancel outstanding work and still tear down
        self.assertEqual(seen, 1)
        self.assertEqual(self.srv.buckets, {}, "break must not leak buckets")

    def test_skip_rules_record_vectors_as_skipped_without_running_them(self):
        runner = new_runner(self.srv.url)
        lst = [pass_vector(), {**pass_vector(), "id": "test-0002", "title": "second"}, {**pass_vector(), "id": "test-0003"}]
        per_vector = {"test-0002": "tracked in issue #42"}
        results = list(runner.run(lst, skip=[
            skip("known bug", ids("test-0001")),
            lambda v: per_vector.get(v["id"]),
            skip("shadowed", ids("test-0001")),  # a later rule never overrides an earlier match
        ]))
        # Concurrency 1: skipped vectors hold their place in the stream.
        self.assertEqual([r.id for r in results], ["test-0001", "test-0002", "test-0003"])
        one, two, three = results
        self.assertEqual((one.outcome, one.reason), ("skipped", "known bug"))
        self.assertEqual((two.outcome, two.reason), ("skipped", "tracked in issue #42"))
        self.assertEqual((three.outcome, three.reason), ("pass", ""))
        for res in (one, two):
            v = next(x for x in lst if x["id"] == res.id)
            self.assertEqual((res.group, res.title, res.tags), (v["group"], v["title"], v["tags"]))
            self.assertEqual(res.steps, [], "skipped vector must not execute steps")
            self.assertEqual(res.duration, 0)
            self.assertEqual(res.runner_error, "")
        self.assertEqual(self.srv.buckets, {})
        # skip() with no filters is a dry run: everything is skipped.
        dry = list(runner.run(lst, skip=[skip("dry run")]))
        self.assertEqual([(r.outcome, r.reason) for r in dry], [("skipped", "dry run")] * 3)
        # An empty string is a valid reason.
        self.assertEqual(list(runner.run(lst[:1], skip=[skip("")]))[0].outcome, "skipped")

    def test_pre_set_cancel_yields_nothing(self):
        ev = threading.Event()
        ev.set()
        self.assertEqual(list(new_runner(self.srv.url).run([pass_vector()], cancel=ev)), [])

    def test_invalid_config_raises(self):
        with self.assertRaisesRegex(ValueError, "endpoint is required"):
            Runner(Config(endpoint="", credentials=Credential("AK", "SK")))
        with self.assertRaisesRegex(ValueError, "credentials is required"):
            Runner(Config(endpoint="http://x", credentials=None))


if __name__ == "__main__":
    unittest.main()
