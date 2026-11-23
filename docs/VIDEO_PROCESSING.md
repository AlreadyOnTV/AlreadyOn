# Video transcoding

Server Settings has one Video Transcoding choice: CPU Only (default), Intel, AMD or NVIDIA. Vendor changes save immediately and request a quick capacity check with a two-minute deadline; they do not start full analysis. The legacy `hardwareDecoding` API field remains compatible, but a vendor-only update enables decoding automatically. CPU Only disables GPU processing.

Images contain checksummed synthetic H.264/HEVC SDR and HEVC PQ/HLG fixtures at representative frame rates. Startup runs a quick check only when no compatible quick or full result exists. Full analysis on the existing four-hour idle schedule compares compatible decode/scale/tone-map/encode pipelines and concurrent streams.

Quick Test replaces only prior quick results. Full Test replaces only prior full results; full results remain authoritative for playback while a newer quick result is retained separately. A materially different manually requested quick result recommends another Full Test; startup, vendor-change, and completed full runs never show that prompt. Completed cases are saved during either run, so Stop Test can preserve partial benefit while cancellation removes incomplete temporary output. Manual tests pause background work. Quick Test pauses active streams directly; Full Test asks for confirmation before doing so. Status refreshes every five seconds and shows one current case line.

Recommendation comparison uses only matching source/encoder/decode/tone-map
pipelines and ignores the expected 1-stream Quick Test versus 2/4-stream Full
Test concurrency difference. It recommends Full Test only when a newer quick
result is verified differently, selects a different output height/frame rate, or
its one-stream speed differs from the full result by at least 25 percent.

A full run targets about ten minutes. Every case retains the same source, output,
driver, decode, tone-map and simultaneous-stream coverage. Each steady-state
sample uses a half-second warmup and a two-second frame window (48 frames at
24 fps or 120 frames at 60 fps); borderline concurrent results retain the
independent confirmation sample and the same 1.3x real-time safety threshold.

Only repeatedly working, sustainable paths qualify. Selection prioritizes measured CPU cost, then GPU engine time when available, then output fidelity and validated simultaneous streams. CPU and GPU cost are normalized to one real-time stream. Reference SSIM compares the encoded candidate with a lossless software reference; it is a fidelity indicator, not a subjective HDR quality guarantee. Missing telemetry is unknown. Driver failures retain staged CPU fallbacks.

Intel's oneVPL dispatcher can load its modern runtime or the bundled Media SDK runtime for older GPUs. Modern and legacy OpenCL implementations coexist behind the ICD loader. GPU decode/encode availability does not imply HDR tone-mapping support; fixed-function and OpenCL paths are measured separately. Host GPU devices/drivers are still required. Hardware results require validation on each vendor's devices; synthetic probes do not certify every media format.

HDR fixtures include static mastering/light metadata. QSV VPP is excluded for sources without that metadata because FFmpeg can otherwise skip tone mapping silently; OpenCL or CPU processing remains available.

Viewing progress or refreshing client network capabilities does not cancel a benchmark. Scheduled tests yield before playback admission. Manual tests block new playback until they stop or complete.
