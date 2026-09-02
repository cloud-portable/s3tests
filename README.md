# S3 Compatibility Test Runners

Runners for the language-independent
[S3 compatibility test vectors](https://github.com/cloud-portable/s3vectors)
(`cloud-portable/s3vectors`). Each runner executes the corpus's `api`-kind
vectors against an S3 endpoint and reports the spec's four outcomes per
vector — `pass`, `fail`, `blocked`, `skipped` — with the stable vector ids
and corpus version needed for cross-runner, cross-target comparison.

This repo follows the s3vectors `packages/` convention: one self-contained
package per language.

| Language | Package | Status |
|---|---|---|
| Go | [packages/go](packages/go) — `github.com/cloud-portable/s3tests/packages/go` | programmatic runner (library) |
| JavaScript | — | planned |
| Python | — | planned |
| Rust | — | planned |

See each package's README for usage. The corpus's normative semantics
(placeholder grammar, matcher semantics, generated data, outcome semantics)
live in the [s3vectors README](https://github.com/cloud-portable/s3vectors);
runners link to it rather than restating it.
