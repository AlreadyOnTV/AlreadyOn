// Show Playback Details: a compact, ordered diagnostic panel combining the
// server's actual pipeline decision with live measurements from the player.
import { useEffect, useState, type RefObject } from "react";
import type Hls from "hls.js";
import { CACHE_TTL, readSnapshot } from "./deviceCache";
import type { AudioDetails, PlaybackSession, VideoDetails } from "./types";

// Retained by Player while HLS loads; transport timing is deliberately not
// shown as transcode speed because those are different measurements.
export interface FragmentStat {
  loadMs: number;
  firstByteMs: number;
  bytes: number;
  seconds: number;
}

// One completed media segment download, in performance.now() milliseconds:
// `first` is when response headers arrived, `end` when the body finished.
export interface Transfer {
  bytes: number;
  first: number;
  end: number;
}

// Segments are 2 s long, so this spans several downloads while staying current.
export const TRANSFER_WINDOW_MS = 10000;
// Below this much total body time the browser's timer cannot resolve the rate.
const MIN_TRANSFER_MS = 10;

interface Live {
  startup: string;
  resolution: string;
  dropped: string;
  buffered: string;
  latency: string;
  speed: string;
  speedInfo: string;
}

const NA = "N/A";
const rate = (bps: number) =>
  bps >= 1e6
    ? `${Number((bps / 1e6).toFixed(1))} Mbps`
    : bps > 0
      ? `${Math.round(bps / 1e3)} kbps`
      : "";
const frameRate = (value: number) =>
  value > 0 ? `${Number(value.toFixed(3))} fps` : "";

function codecName(codec: string) {
  return (
    (
      {
        h264: "H.264",
        hevc: "H.265",
        eac3: "E-AC-3",
        ac3: "AC-3",
        truehd: "TrueHD",
        aac: "AAC",
        av1: "AV1",
        vp9: "VP9",
        dts: "DTS",
      } as Record<string, string>
    )[codec.toLowerCase()] ?? codec.toUpperCase()
  );
}

function bitDepth(pixelFormat?: string) {
  if (!pixelFormat) return "";
  const explicit =
    pixelFormat.match(/p(\d{2})(?:le|be)?$/i)?.[1] ??
    pixelFormat.match(/^p0(\d{2})(?:le|be)$/i)?.[1];
  if (explicit) return `${Number(explicit)}-bit`;
  return /(?:yuv|nv12)/i.test(pixelFormat) ? "8-bit" : "";
}

function videoLine(video: VideoDetails, approximateBitrate = false) {
  const codec =
    codecName(video.codec) + (video.profile ? ` ${video.profile}` : "");
  const size =
    video.width > 0 && video.height > 0 ? `${video.width}x${video.height}` : "";
  const bitrate =
    video.bitrate > 0
      ? `${approximateBitrate ? "≈" : ""}${rate(video.bitrate)}`
      : "";
  return (
    [
      codec,
      size,
      frameRate(video.fps),
      bitDepth(video.pixelFormat),
      video.dynamicRange,
      bitrate,
    ]
      .filter(Boolean)
      .join(" / ") || NA
  );
}

function audioLine(audio?: AudioDetails) {
  if (!audio) return NA;
  const codec =
    codecName(audio.codec) + (audio.profile ? ` ${audio.profile}` : "");
  const channels = audio.layout
    ? `${audio.layout} (${audio.channels} ch)`
    : audio.channels > 0
      ? `${audio.channels} ch`
      : "";
  return (
    [
      codec,
      channels,
      audio.sampleRate > 0 ? `${audio.sampleRate / 1000} kHz` : "",
      audio.bitrate > 0 ? rate(audio.bitrate) : "",
    ]
      .filter(Boolean)
      .join(" / ") || NA
  );
}

function outputPrefix(method: string) {
  if (method === "Direct") return "Direct Play";
  if (method === "Copied") return "Remux";
  return "Transcode";
}

function pipelineStage(
  stage: string | undefined,
  codec: string,
  method: string,
) {
  if (method === "Direct") return "None / Direct Play";
  if (method === "Copied") return "None / Remux";
  if (!stage || stage === "Not Used") return NA;
  if (stage === "Software (CPU)")
    return `Software - FFmpeg ${codecName(codec)}`;
  return `Hardware - ${stage.replace(/ \(GPU\)$/, "")} ${codecName(codec)}`;
}

function elapsed(value: number) {
  if (!Number.isFinite(value) || value < 0) return NA;
  return value < 1000
    ? `${Math.round(value)} ms`
    : `${Number((value / 1000).toFixed(1))} s`;
}

// Loopback destinations prove the media server is on this browser's machine.
// A LAN address or arbitrary hostname cannot establish that reliably.
function isLocalMeasurement(origin: string) {
  try {
    const host = new URL(origin).hostname.toLowerCase();
    return (
      host === "localhost" ||
      host.endsWith(".localhost") ||
      host === "[::1]" ||
      host === "::1" ||
      /^127\.(?:\d{1,3}\.){2}\d{1,3}$/.test(host)
    );
  } catch {
    return false;
  }
}

// Rate while bytes were actually flowing: total bytes over the union of the
// body-transfer intervals. Idle gaps between segments and request round trips
// are excluded, so this reflects the link, not the stream's bitrate.
function transferSpeed(transfers: Transfer[], origin: string) {
  if (transfers.length === 0) return null;
  const now = performance.now();
  const recent = transfers.filter((t) => now - t.end <= TRANSFER_WINDOW_MS);
  const idle = recent.length === 0;
  const sample = idle ? transfers.slice(-1) : recent;
  const intervals = sample
    .map((t) => [t.first, t.end])
    .sort((a, b) => a[0] - b[0]);
  let busyMs = 0;
  let [start, end] = intervals[0];
  for (const [s, e] of intervals.slice(1)) {
    if (s > end) {
      busyMs += end - start;
      start = s;
    }
    end = Math.max(end, e);
  }
  busyMs += end - start;
  const bytes = sample.reduce((sum, t) => sum + t.bytes, 0);
  const floor = busyMs < MIN_TRANSFER_MS;
  const mbps = Math.min(
    (bytes * 8) / (Math.max(busyMs, MIN_TRANSFER_MS) * 1000),
    100000,
  );
  const tags = [isLocalMeasurement(origin) ? "Local" : "", idle ? "Idle" : ""]
    .filter(Boolean)
    .join(", ");
  return {
    speed: `${floor ? "≥ " : ""}${mbps.toFixed(2)} Mbps${tags ? ` (${tags})` : ""}`,
    speedInfo: `Measured from the last ${sample.length} video segment download${sample.length === 1 ? "" : "s"} (${(bytes / 1e6).toFixed(1)} MB in ${Math.round(busyMs)} ms of transfer) from ${origin}.${floor ? " Transfers finished too quickly to time precisely; the actual speed is at least this value." : ""}${idle ? " No segment was downloaded in the last 10 seconds, so this is the last measurement." : ""} Localhost traffic does not cross Wi-Fi.`,
  };
}

function measure(
  video: HTMLVideoElement | null,
  hls: Hls | null,
  transfers: Transfer[],
): Live {
  const network = readSnapshot()?.network;
  const measured =
    network &&
    network.capturedAt <= Date.now() &&
    Date.now() - network.capturedAt < CACHE_TTL;
  const origin = hls?.url ? new URL(hls.url, location.href).origin : "";
  const live = hls ? transferSpeed(transfers, origin) : null;
  const quality = video?.getVideoPlaybackQuality?.();
  const hasVideo = !!video;
  const buffered =
    hasVideo && video.buffered.length > 0
      ? Math.max(
          0,
          video.buffered.end(video.buffered.length - 1) - video.currentTime,
        )
      : 0;
  const latencySeconds =
    hls && Number.isFinite(hls.latency) && hls.latency >= 0
      ? hls.latency
      : null;
  return {
    startup: video?.dataset.firstPictureMs
      ? elapsed(Number(video.dataset.firstPictureMs))
      : "Starting…",
    resolution:
      video && video.videoWidth > 0
        ? `${video.videoWidth}x${video.videoHeight}`
        : NA,
    dropped: quality
      ? `${quality.droppedVideoFrames}${quality.totalVideoFrames > 0 ? ` / ${quality.totalVideoFrames} rendered` : ""}`
      : NA,
    buffered: hasVideo ? `${buffered.toFixed(1)} s` : NA,
    // hls.js latency is the playhead's distance behind the current HLS live edge.
    latency: latencySeconds == null ? NA : elapsed(latencySeconds * 1000),
    // HLS streams report their own segment downloads. Direct Play and native
    // HLS expose no per-download timing, so they fall back to the cached probe.
    // Localhost measures loopback delivery; it cannot measure the Wi-Fi link.
    ...(live ??
      (hls
        ? {
            speed: "Measuring…",
            speedInfo: "Waiting for the first video segment download.",
          }
        : {
            speed: measured
              ? `${network.mbps.toFixed(2)} Mbps (Probe${isLocalMeasurement(network.origin) ? ", Local" : ""})`
              : NA,
            speedInfo: measured
              ? `Browser download probe from ${network.origin}, measured ${new Date(network.capturedAt).toLocaleString()}. This playback method exposes no live download timing. Localhost traffic does not cross Wi-Fi.`
              : "Browser download measurement unavailable.",
          })),
  };
}

export function PlaybackDetails({
  session,
  videoRef,
  hlsRef,
  transfersRef,
}: {
  session: PlaybackSession;
  videoRef: RefObject<HTMLVideoElement | null>;
  hlsRef: RefObject<Hls | null>;
  transfersRef: RefObject<Transfer[]>;
}) {
  const [live, setLive] = useState<Live>({
    startup: "Starting…",
    resolution: NA,
    dropped: NA,
    buffered: NA,
    latency: NA,
    speed: NA,
    speedInfo: "Browser download measurement unavailable.",
  });
  useEffect(() => {
    const id = window.setInterval(
      () =>
        setLive(
          measure(videoRef.current, hlsRef.current, transfersRef.current),
        ),
      1000,
    );
    return () => window.clearInterval(id);
  }, [videoRef, hlsRef, transfersRef]);

  const details = session.details;
  if (!details) return null;
  const outputVideo = details.outputVideo;
  const rows: [string, string][] = [
    ["File Name", details.fileName || NA],
    ["Source Video", videoLine(details.sourceVideo, true)],
    [
      "Output Video",
      `${outputPrefix(outputVideo.method)} - ${videoLine(outputVideo)}`,
    ],
    [
      "Video Decoder",
      pipelineStage(
        details.pipeline.decoder,
        details.sourceVideo.codec,
        outputVideo.method,
      ),
    ],
    [
      "Video Encoder",
      pipelineStage(
        details.pipeline.encoder,
        outputVideo.codec,
        outputVideo.method,
      ),
    ],
    ["Source Audio", audioLine(details.sourceAudio)],
    [
      "Output Audio",
      details.outputAudio
        ? `${outputPrefix(details.outputAudio.method)} - ${audioLine(details.outputAudio)}`
        : NA,
    ],
    ["Startup Time", live.startup],
    ["Resolution", live.resolution],
    ["Dropped Frames", live.dropped],
    ["Local Buffer", live.buffered],
    ["Live Delay", live.latency],
    ["Download Speed", live.speed],
    [
      "CPU Util",
      session.plan.mode === "direct"
        ? "0.0%"
        : session.runtime?.cpuUtil != null
          ? `${session.runtime.cpuUtil.toFixed(1)}%`
          : NA,
    ],
  ];
  return (
    <div
      className="playback-details"
      role="status"
      aria-label="Playback Details"
    >
      <dl>
        {rows.map(([name, value]) => (
          <div key={name}>
            <dt aria-label={name}>{name}</dt>
            <dd title={name === "Download Speed" ? live.speedInfo : value}>
              {value}
            </dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
