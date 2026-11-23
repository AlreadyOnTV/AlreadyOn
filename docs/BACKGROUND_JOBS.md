# Background work

Database migrations finish before HTTPS starts. Configuration, TLS and stored sessions persist.

## Startup and priority

After the website becomes available, **Performance Benchmarks** runs representative startup calibration within 120 seconds unless compatible quick or full results exist. Synthetic SDR, PQ and HLG fixtures use fixed frame windows after warm-up. Thread choices and concurrent stream counts are measured separately; every stream must exceed the safety margin, with borderline results confirmed. Automatic capacity starts at two conversion slots, subject to memory limits, until compatible results exist. Results stay usable across restarts and image updates while the selected transcoding device stays the same. Idle benchmarks refresh old results without expiring them. Playback authorization and tuning return 503 with `code: server_starting` and `Retry-After: 2`; the WebUI says **Server starting, please wait.** Completed measurements survive interruption; shutdown cancels the group.

The library pipeline follows: scan/probe, TMDb enrichment, free-provider metadata, channel population and schedule generation. It runs alongside playback under resource-pressure limits. Benchmark reservations pause library subprocesses and yield cooperative work at safe checkpoints outside database transactions.

Keyframe and enabled loudness analysis have lowest priority. One analysis subprocess is eligible at a time, with keyframes ahead of loudness. Both pause during library work or benchmarking. Paused probes retain their execution-time budget, so a long benchmark does not cause a false timeout. Matching results are reused.

Health, authentication, metrics, playback cleanup, DNS and certificate services continue independently. Resource-pressure and CPU-budget controls remain active; priority does not bypass them.

## Recurring work

At 30 minutes after startup, a Full Test runs once if compatible Full results are missing and playback and benchmarks are idle.

Performance benchmarks run at local **00:00, 04:00, 08:00, 12:00, 16:00 and 20:00**, following the configured server timezone. These idle runs compare complete pipelines across the prepared fixtures and supported output rungs, refining representative concurrency. A due run waits while playback is active. New playback cancels a recurring benchmark and waits for its subprocess teardown before admission. Interrupted runs retain previous valid measurements. Missed runs coalesce.

Vendor changes request a two-minute check. Manual Quick and Full Tests replace only their own results, pause background work and block playback. Quick pauses streams immediately; Full requires confirmation. Stop Test retains completed cases. Settings polls every five seconds and recommends Full only when a newer manual quick result differs materially. Saved HDR results apply to the measured conversion pipeline for the selected transcoding device.

**Refresh Library** controls the full recurring library pipeline, even when discovery finds no new files. Its interval starts after the preceding run finishes. A due refresh waits for benchmarking or another library update. Settings-triggered work remains durable and serialized with library refresh.

## Validation

Tests use temporary storage and controlled jobs/clocks for ordering, four-hour/DST boundaries, admission races, cancellation, priority and pause/resume. The browser harness runs `PLAYBACK_TEST_ONLY=startup-benchmarks make test-e2e` to exercise real startup rejection, website health, the waiting message and automatic playback recovery. Preview tests intercept management requests so they do not alter existing accounts or libraries.
