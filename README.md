# S3 Compatibility Test Runners

Runners for the language-independent
[S3 compatibility test vectors](https://github.com/cloud-portable/s3vectors)
(`cloud-portable/s3vectors`). Each runner executes the corpus's `api`-kind
vectors against an S3 endpoint and reports the spec's four outcomes per
vector — `pass`, `fail`, `blocked`, `skipped` — with the stable vector ids
and corpus version needed for cross-runner, cross-target comparison.

| Language | Package | Status |
|---|---|---|
| Go | [packages/go](packages/go) — `github.com/cloud-portable/s3tests/packages/go` | Library and CLI |
| JavaScript | [packages/js](packages/js) — `@cloud-portable/s3tests` | Library and CLI |
| Python | [packages/python](packages/python) — `cloud-portable-s3tests` | Library and CLI |
| Rust | — | planned |

See each package's README for usage. The corpus's normative semantics
(placeholder grammar, matcher semantics, generated data, outcome semantics)
live in the [s3vectors README](https://github.com/cloud-portable/s3vectors);
runners link to it rather than restating it.

## Example Report

<img width="6744" height="4692" alt="Image" src="https://github.com/user-attachments/assets/3f629a1c-68dc-46bc-8d4f-32198a69f4ed" />

## License

Apache-2.0 OR MIT
