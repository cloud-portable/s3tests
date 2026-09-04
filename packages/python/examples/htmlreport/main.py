"""Runs the compatibility corpus against an S3 endpoint and writes the HTML
report — the example consumer of the library API: select vectors with
filters, stream run() results straight into a reporter.

    .venv/bin/python examples/htmlreport/main.py --target "MinIO (local docker)" -o report.html
    .venv/bin/python examples/htmlreport/main.py --tags tier-1 -o report-tier-1.html
"""

import argparse
from datetime import datetime, timezone

from cloud_portable_s3tests import Config, Credential, Runner, apply_filters, groups, tags, vectors
from cloud_portable_s3tests.report import Meta, html

parser = argparse.ArgumentParser()
parser.add_argument("--endpoint", default="http://127.0.0.1:9000")
parser.add_argument("--access-key", default="minioadmin")
parser.add_argument("--secret-key", default="minioadmin")
parser.add_argument("--target")
parser.add_argument("--groups")
parser.add_argument("--tags")
parser.add_argument("--concurrency", type=int, default=4)
parser.add_argument("-o", default="report.html")
args = parser.parse_args()

runner = Runner(Config(
    endpoint=args.endpoint,
    credentials=Credential(args.access_key, args.secret_key),
    concurrency=args.concurrency,
))

filters = []
properties = {}
if args.groups:
    filters.append(groups(*args.groups.split(",")))
    properties["groups"] = args.groups
if args.tags:
    filters.append(tags(*args.tags.split(",")))
    properties["tags"] = args.tags
selected = apply_filters(vectors(), *filters)
if not selected:
    raise SystemExit("no vectors selected")

with open(args.o, "wb") as f:
    html.write(f, runner.run(selected), Meta(
        corpus_version=runner.corpus_version(),
        target=args.target or args.endpoint,
        properties=properties,
        generated_at=datetime.now(timezone.utc),
    ))
print(f"wrote {args.o} ({len(selected)} vectors, corpus {runner.corpus_version()})")
