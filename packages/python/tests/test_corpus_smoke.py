"""The offline corpus smoke test dry-runs every api vector — no network —
proving the runner can process it: every placeholder resolves, every
operation is a known boto3 method with coercible params, every $matches regex
compiles, every capture path parses, every content descriptor and dataset
materializes. It is the drift alarm for corpus version bumps."""

import json
import re
import unittest

from cloud_portable_s3tests import vectors
from cloud_portable_s3tests._coerce import build_input
from cloud_portable_s3tests._config import Config, Credential, build_client, with_defaults
from cloud_portable_s3tests._dispatch import supported
from cloud_portable_s3tests._interp import Scope
from cloud_portable_s3tests._jsonpath import parse as parse_path
from cloud_portable_s3tests._match import compile_regex, content_value
from cloud_portable_s3tests._vdata import DataCache

# Known runner limitations, as "<id>: <problem>" strings. botocore still models
# PutBucketLifecycle (which the Go and JS SDKs dropped), so the Python runner
# currently has none.
ALLOWED: set[str] = set()


class TestCorpusSmoke(unittest.TestCase):
    def test_corpus_smoke_every_api_vector_is_executable(self):
        all_ = vectors()
        self.assertGreater(len(all_), 1000, f"only {len(all_)} api vectors — corpus load broken?")
        cfg = with_defaults(Config(endpoint="http://smoke.invalid:9000", credentials=Credential("SMOKE", "SMOKE")))
        client = build_client(cfg, cfg.credentials)
        problems = [f"{v['id']}: {p}" for v in all_ for p in smoke_vector(client, v)]
        unexpected = [p for p in problems if p not in ALLOWED]
        self.assertEqual(unexpected, [])
        self.assertEqual(len(problems), len(ALLOWED), f"expected exactly the known problem(s), found: {problems}")


def smoke_vector(client, v):
    problems = []
    fail = problems.append
    cache = DataCache(v.get("data"))
    scope = Scope(env={"endpoint": "http://smoke.invalid:9000", "region": "us-east-1"}, data=cache.derived)

    # Register prerequisite resource attributes exactly as the runner would.
    for i, prereq in enumerate(v.get("prerequisites") or []):
        if "$bucket" in prereq:
            scope.res[prereq["$bucket"]["handle"]] = {"name": "smoke-bucket"}
        elif "$object" in prereq:
            p = prereq["$object"]
            scope.res[p["handle"]] = {"key": p["key"], "etag": '"d41d8cd98f00b204e9800998ecf8427e"', "versionId": "smoke-version"}
            if "body" in p:
                try:
                    content_value(scope.value(p["body"]), cache.bytes)
                except Exception as err:  # noqa: BLE001
                    fail(f"object prerequisite {p['handle']} body: {err}")
        elif "$credential" in prereq:
            scope.res[prereq["$credential"]["handle"]] = {"accessKeyId": "SMOKEKEY", "canonicalId": "smoke-canonical", "displayName": "smoke"}
        else:
            fail(f"prerequisite {i} has no union key")

    # Pre-seed every ${cap.*} reference; the value must survive every context
    # captures are re-injected into, including timestamp params.
    for m in re.finditer(r"\$\{cap\.([A-Za-z0-9_-]+)\}", json.dumps(v)):
        scope.cap[m.group(1)] = "2026-01-01T00:00:00Z"

    for i, step in enumerate(v.get("steps") or []):
        step_no = i + 1
        try:
            interpolated = scope.value(step)
        except Exception as err:  # noqa: BLE001
            fail(f"step {step_no}: {err}")
            continue
        if "$operation" in interpolated:
            op = interpolated["$operation"]
            if not supported(client, op["name"]):
                fail(f"step {step_no}: operation {op['name']} is not supported by boto3")
            else:
                try:
                    build_input(client.meta.service_model.operation_model(op["name"]), op.get("params") or {}, cache.bytes)
                except Exception as err:  # noqa: BLE001
                    fail(f"step {step_no}: {err}")
            smoke_expect(op.get("expect"), cache, fail, step_no)
            smoke_capture(op.get("capture"), fail, step_no)
        elif "$http" in interpolated:
            st = interpolated["$http"]
            if "body" in st:
                try:
                    content_value(st["body"], cache.bytes)
                except Exception as err:  # noqa: BLE001
                    fail(f"step {step_no}: body: {err}")
            smoke_expect(st.get("expect"), cache, fail, step_no)
            smoke_capture(st.get("capture"), fail, step_no)
        else:
            fail(f"step {step_no} has no union key")
    return problems


def smoke_capture(spec, fail, step_no):
    for name, path in (spec or {}).items():
        try:
            parse_path(path)
        except Exception as err:  # noqa: BLE001
            fail(f"step {step_no}: capture {name}: {err}")


def smoke_expect(exp, cache, fail, step_no):
    if exp is None:
        return
    compile_matchers(exp.get("error"), fail, step_no)
    compile_matchers(exp.get("response"), fail, step_no)
    for matcher in (exp.get("headers") or {}).values():
        compile_matchers(matcher, fail, step_no)
    if "body" in exp:
        b = exp["body"]
        is_digest = isinstance(b, dict) and ("$size" in b or "$md5" in b or "$sha256" in b)
        if not is_digest:
            try:
                content_value(b, cache.bytes)
            except Exception as err:  # noqa: BLE001
                fail(f"step {step_no}: expect.body: {err}")


def compile_matchers(v, fail, step_no):
    if isinstance(v, list):
        for e in v:
            compile_matchers(e, fail, step_no)
    elif isinstance(v, dict):
        for k, e in v.items():
            if k == "$matches" and isinstance(e, str):
                try:
                    compile_regex(e)
                except Exception as err:  # noqa: BLE001
                    fail(f"step {step_no}: $matches {json.dumps(e)}: {err}")
                continue
            compile_matchers(e, fail, step_no)


if __name__ == "__main__":
    unittest.main()
