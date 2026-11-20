# Background work

Database migrations complete before HTTPS, authentication and the WebUI start. Existing configuration, TLS identity and stored sessions are retained.

## Startup and priority

After the website becomes available, **Performance Benchmarks** runs thread tuning, capacity/source measurements and applicable HDR measurements sequentially, then recalculates conversion limits. Playback authorization and tuning return HTTP 503 with `code: server_starting` and `Retry-After: 2` until the startup group finishes. The WebUI displays **Server starting, please wait.** and retries automatically. Operational measurement failures retain previous valid limits; shutdown cancels the group.

The library pipeline follows: scan/probe, TMDb enrichment, free-provider metadata, channel population and schedule generation. It runs alongside playback under resource-pressure limits. Benchmark reservations pause library subprocesses and yield cooperative work at safe checkpoints outside database transactions.

Keyframe and enabled loudness analysis have lowest priority. One analysis subprocess is eligible at a time, with keyframes ahead of loudness. Both pause during library work or benchmarking. Paused probes retain their execution-time budget, so a long benchmark does not cause a false timeout. Completed fingerprint-matching results are reused.

Health, authentication, metrics, playback cleanup, DNS and certificate services continue independently. Resource-pressure and CPU-budget controls remain active; priority does not bypass them.

## Recurring work

Performance benchmarks run at local **00:00, 04:00, 08:00, 12:00, 16:00 and 20:00**, following the configured server timezone. A due run waits while streams or playback admissions are active. New playback cancels a recurring benchmark and waits for its subprocess teardown before admission. Interrupted runs retain previous valid measurements. Pending requests are coalesced; no catch-up burst runs for missed boundaries.

Explicit measurement and encoder-setting changes use the same group. Hardware diagnostics also exclude lower-priority background work. Process-local HDR results remain usable while idle-only remeasurement is deferred, subject to exact pipeline identity; they are never restored from disk after restart.

**Refresh Library** controls the full recurring library pipeline, even when discovery finds no new files. Its interval starts after the preceding run finishes. A due refresh waits for benchmarking or another library update. Settings-triggered work remains durable and serialized with library refresh. There are no separate hourly schedule-regeneration or conversion-limit jobs bypassing coordination.

## Validation

Tests use temporary storage and controlled jobs/clocks for ordering, four-hour/DST boundaries, admission races, cancellation, priority and pause/resume. The isolated browser harness runs `PLAYBACK_TEST_ONLY=startup-benchmarks make test-e2e` to exercise real startup rejection, website health, the waiting message and automatic playback recovery. Preview tests intercept management requests so they do not alter existing accounts or libraries.
