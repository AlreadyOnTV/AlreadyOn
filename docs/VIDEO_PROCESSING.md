# Video transcoding

Server Settings has one Video Transcoding choice: CPU Only (default), Intel, AMD or NVIDIA. Vendor changes save immediately and request a quick capacity check with a two-minute deadline; they do not start full analysis. The legacy `hardwareDecoding` API field remains compatible, but a vendor-only update enables decoding automatically. CPU Only disables GPU processing.

Startup and Quick Test measure representative 360p SDR conversions. Full Test also measures 240p and higher rungs. The server-wide slot ceiling follows validated 360p concurrency. Admission budgets mixed conversions using their measured demand and retains memory headroom.

Auto conversions try 720p SDR first and fall back to measured lower rungs when needed. WebUI, phone and TV sample playback every five seconds, targeting three to five seconds buffered. Two pressured samples lower the quality. Three healthy samples request a higher rung within source and client limits. Higher Auto rungs can yield capacity after a refused tune. The current rendition plays during preparation.

Quick and Full results remain separate. See Test Results shows the default backend, FFmpeg encoder and slot ceiling. Stop Test retains completed cases. Manual Full Test asks before pausing active streams.

Recommendation comparison uses only matching source/encoder/decode/tone-map
pipelines and ignores concurrency differences. It recommends Full Test only when a newer quick
result is verified differently, selects a different output height/frame rate, or
its one-stream speed differs from the full result by at least 25 percent.

Full Test covers source, output, driver, decode, tone-map and concurrent workloads. Each steady-state
sample uses a half-second warmup and a two-second frame window (48 frames at
24 fps or 120 frames at 60 fps); borderline concurrent results retain the
independent confirmation sample and the same 1.3x real-time safety threshold.

Only repeatedly working, sustainable paths qualify. Selection prioritizes measured CPU cost, then GPU engine time when available, then output fidelity and validated simultaneous streams. CPU and GPU cost are normalized to one real-time stream. Reference SSIM compares the encoded candidate with a lossless software reference; it is a fidelity indicator, not a subjective HDR quality guarantee. Missing telemetry is unknown. Driver failures retain staged CPU fallbacks.

Intel's oneVPL dispatcher can load its modern runtime or the bundled Media SDK runtime for older GPUs. Modern and legacy OpenCL implementations coexist behind the ICD loader. GPU decode/encode availability does not imply HDR tone-mapping support; fixed-function and OpenCL paths are measured separately. Host GPU devices/drivers are still required. Hardware results require validation on each vendor's devices; synthetic probes do not certify every media format.

HDR fixtures include static mastering/light metadata. QSV VPP is excluded for sources without that metadata because FFmpeg can otherwise skip tone mapping silently; OpenCL or CPU processing remains available.

Viewing progress or refreshing client network capabilities does not cancel a benchmark. Scheduled tests yield before playback admission. Manual tests block new playback until they stop or complete.
