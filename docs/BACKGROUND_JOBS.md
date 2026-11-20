# Background work

Database migrations complete before HTTPS, authentication and the WebUI start. Existing configuration, TLS identity and stored sessions are retained.

## Startup and priority

After the website becomes available, **Performance Benchmarks** runs representative startup calibration with a 120-second deadline. Synthetic SDR, PQ and HLG inputs are baked into a cached Docker layer (approximately 40 MB). Fixed wall-clock frame windows exclude warm-up; CPU thread choices and simultaneous stream counts are measured separately. Every concurrent stream must exceed the real-time safety margin, with an additional confirmation near the threshold. Unfinished or failed paths remain visibly unverified with conservative limits. A fingerprint covering fixtures, FFmpeg, hardware, drivers, resource limits and relevant settings permits reuse for up to 24 hours, with concurrency confirmation on startup. The Dashboard reports limits per tested pipeline rather than extrapolating from core count. Playback authorization and tuning return HTTP 503 with `code: server_starting` and `Retry-After: 2` until the startup group finishes. The WebUI displays **Server starting, please wait.** and retries automatically. Interrupted runs retain matching completed measurements; shutdown cancels the group.

The library pipeline follows: scan/probe, TMDb enrichment, free-provider metadata, channel population and schedule generation. It runs alongside playback under resource-pressure limits. Benchmark reservations pause library subprocesses and yield cooperative work at safe checkpoints outside database transactions.

Keyframe and enabled loudness analysis have lowest priority. One analysis subprocess is eligible at a time, with keyframes ahead of loudness. Both pause during library work or benchmarking. Paused probes retain their execution-time budget, so a long benchmark does not cause a false timeout. Matching results are reused.

Health, authentication, metrics, playback cleanup, DNS and certificate services continue independently. Resource-pressure and CPU-budget controls remain active; priority does not bypass them.

## Recurring work

Performance benchmarks run at local **00:00, 04:00, 08:00, 12:00, 16:00 and 20:00**, following the configured server timezone. These idle runs retain the full source/output matrix and refine representative concurrency. A due run waits while streams or playback admissions are active. New playback cancels a recurring benchmark and waits for its subprocess teardown before admission. Interrupted runs retain previous valid measurements. Missed runs coalesce.

Explicit measurement and encoder-setting changes use the same group. Hardware diagnostics also exclude lower-priority background work. Process-local HDR results remain usable while idle-only remeasurement is deferred, subject to exact pipeline identity; they are never restored from disk after restart.

**Refresh Library** controls the full recurring library pipeline, even when discovery finds no new files. Its interval starts after the preceding run finishes. A due refresh waits for benchmarking or another library update. Settings-triggered work remains durable and serialized with library refresh. There are no separate hourly schedule-regeneration or conversion-limit jobs bypassing coordination.

## Validation

Tests use temporary storage and controlled jobs/clocks for ordering, four-hour/DST boundaries, admission races, cancellation, priority and pause/resume. The browser harness runs `PLAYBACK_TEST_ONLY=startup-benchmarks make test-e2e` to exercise real startup rejection, website health, the waiting message and automatic playback recovery. Preview tests intercept management requests so they do not alter existing accounts or libraries.
