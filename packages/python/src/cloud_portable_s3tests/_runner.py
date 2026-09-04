"""The Runner: executes vectors against one endpoint, yielding results from a
concurrency-limited worker pool."""

from __future__ import annotations

import queue
import threading
from typing import Iterable, Iterator, Optional

from cloud_portable_s3vectors import manifest

from ._config import IDENTITY_MAIN, Config, Identities, build_client, with_defaults
from ._provision import Target, default_provisioner
from ._run import Runtime
from ._skip import SkipFunc, skip_reason
from ._vector import new_result, run_vector
from ._result import VectorResult


class _Cancel:
    """Union of the caller's cancel event and the generator's own stop flag."""

    def __init__(self, outer: Optional[threading.Event]) -> None:
        self._outer = outer
        self._stop = threading.Event()

    def stop(self) -> None:
        self._stop.set()

    def is_set(self) -> bool:
        return self._stop.is_set() or (self._outer is not None and self._outer.is_set())


class Runner:
    """Executes corpus vectors against one S3 endpoint."""

    def __init__(self, config: Config) -> None:
        """Raises ValueError when config.endpoint or config.credentials is missing."""
        self.cfg = with_defaults(config)
        self.identities = Identities(self.cfg)
        main_client = build_client(self.cfg, self.cfg.credentials)
        self.identities.clients[IDENTITY_MAIN] = main_client
        self.target = Target(endpoint=self.cfg.endpoint, region=self.cfg.region, client=main_client)
        self.rt = Runtime(cfg=self.cfg, identities=self.identities, target=self.target, default_provisioner=default_provisioner)

    def corpus_version(self) -> str:
        """The vector corpus snapshot this runner executes — stamp it into reports."""
        return str(manifest()["version"])

    def run(
        self,
        vectors: Iterable[dict],
        *,
        skip: Optional[Iterable[SkipFunc]] = None,
        cancel: Optional[threading.Event] = None,
    ) -> Iterator[VectorResult]:
        """Execute the given vectors, yielding one result per vector in
        completion order (identical to the given order when concurrency is 1).
        Selection happens before run — see vectors() and apply_filters().
        Vectors matched by a rule in ``skip`` are not executed but still yield
        a result with outcome 'skipped' and the rule's reason (see skip()).

        Breaking out of the loop, or setting ``cancel``, stops the run: not-yet
        -started vectors never run and in-flight vectors stop at their next
        step boundary (a call already in flight completes or times out; their
        resource teardown still runs on its own deadline) — a stopped stream
        is therefore incomplete. The generator does not return until all
        in-flight work has wound down.
        """
        vectors = list(vectors)
        rules = list(skip or [])
        cxl = _Cancel(cancel)
        out: queue.Queue = queue.Queue()
        lock = threading.Lock()
        counter = [0]
        done = object()
        n = min(self.cfg.concurrency, len(vectors))

        def worker() -> None:
            try:
                while True:
                    with lock:
                        i = counter[0]
                        counter[0] += 1
                    if i >= len(vectors) or cxl.is_set():
                        return
                    reason = skip_reason(rules, vectors[i])
                    res = run_vector(self.rt, vectors[i], cxl) if reason is None else new_result(vectors[i], "skipped", reason)
                    if not cxl.is_set():
                        out.put(res)
            finally:
                out.put(done)

        workers = [threading.Thread(target=worker, name=f"s3tests-worker-{k}", daemon=True) for k in range(n)]
        for t in workers:
            t.start()
        try:
            remaining = n
            while remaining > 0:
                item = out.get()
                if item is done:
                    remaining -= 1
                    continue
                yield item
        finally:
            # Early break or external cancel: stop outstanding work and wait
            # for in-flight vectors (and their teardowns) to finish.
            cxl.stop()
            for t in workers:
                t.join()


def audit_client(config: Config):
    """A boto3 S3 client configured the way the runner's own clients are —
    useful for auditing/tooling around a run (e.g. listing leaked buckets)."""
    cfg = with_defaults(config)
    return build_client(cfg, cfg.credentials)
