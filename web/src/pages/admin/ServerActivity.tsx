import { useEffect, useState } from "react";
import { apiFetch } from "../../api/access";
import { mediaTitle } from "../mediaTitle";
import "./ServerActivity.css";

type Session = {
  id: string;
  channel: string;
  video: string;
  program?: string;
  user: string;
  profile: string;
  sourceIp: string;
  device: string;
  mode: string;
  encoder: string;
  startedAt: string;
  playbackPositionSeconds?: number;
  playbackDurationSeconds?: number;
  bandwidthMbps: number | null;
  appVersion?: string;
  commercial: boolean;
  paused: boolean;
  sourceVideo: VideoFormat;
  outputVideo: VideoFormat;
  sourceAudio?: AudioFormat;
  outputAudio?: AudioFormat;
};
type VideoFormat = {
  codec: string;
  profile?: string;
  width: number;
  height: number;
  fps: number;
  pixelFormat?: string;
};
type AudioFormat = {
  codec: string;
  profile?: string;
  channels: number;
  layout?: string;
};
type Process = {
  id: string;
  name: string;
  runtimeSeconds: number;
  percent: number | null;
  completed: number;
  total: number;
  unit: string;
  currentFile?: string;
};
type Activity = {
  sessions: Session[];
  processes: Process[];
  transcodeSlots?: { active: number; limit: number };
};
// Explicit filenames retain their release tags; the visible cap includes the ellipsis.
const clip = (value: string) => {
  const chars = Array.from(value);
  return chars.length > 50 ? chars.slice(0, 49).join("") + "…" : value;
};
function runtime(seconds: number) {
  const n = Math.floor(seconds);
  return n >= 3600
    ? `${Math.floor(n / 3600)}h ${Math.floor((n % 3600) / 60)}m ${n % 60}s`
    : `${Math.floor(n / 60)}m ${n % 60}s`;
}
function playbackTime(positionSeconds?: number, durationSeconds?: number) {
  if (
    positionSeconds == null ||
    !Number.isFinite(positionSeconds) ||
    durationSeconds == null ||
    !Number.isFinite(durationSeconds) ||
    durationSeconds <= 0
  )
    return "Unavailable";
  const timestamp = (seconds: number) => {
    const minutes = Math.floor(seconds / 60);
    return `${String(Math.floor(minutes / 60)).padStart(2, "0")}:${String(minutes % 60).padStart(2, "0")}`;
  };
  return `${timestamp(Math.min(durationSeconds, Math.max(0, positionSeconds)))}/${timestamp(durationSeconds)}`;
}
function videoFormat(video: VideoFormat) {
  if (!video.codec || video.width <= 0 || video.height <= 0)
    return "Unavailable";
  const codec =
    (
      { h264: "H.264", hevc: "H.265", vp9: "VP9", av1: "AV1" } as Record<
        string,
        string
      >
    )[video.codec] ?? video.codec.toUpperCase();
  const depth = video.pixelFormat?.match(/p(\d+)/)?.[1] ?? "8";
  const profile = video.profile ? ` ${video.profile}` : "";
  const fps = video.fps > 0 ? ` / ${Number(video.fps.toFixed(3))} fps` : "";
  return `${codec}${profile} / ${video.width}x${video.height}${fps} / ${depth}-bit`;
}
function audioFormat(audio?: AudioFormat) {
  if (!audio) return "No audio";
  const codec =
    (
      {
        eac3: "E-AC-3",
        ac3: "AC-3",
        truehd: "TrueHD",
        aac: "AAC",
        dts: "DTS",
        flac: "FLAC",
        opus: "Opus",
      } as Record<string, string>
    )[audio.codec] ?? audio.codec.toUpperCase();
  const profile =
    audio.codec === "eac3"
      ? ` Dolby Digital Plus${/atmos|joc/i.test(audio.profile ?? "") ? " + Dolby Atmos" : ""}`
      : audio.profile
        ? ` ${audio.profile}`
        : "";
  const layout =
    audio.layout?.replace("(side)", "") ||
    ({ 2: "stereo", 6: "5.1", 8: "7.1" } as Record<number, string>)[
      audio.channels
    ] ||
    `${audio.channels} ch`;
  return `${codec}${profile} / ${layout}`;
}
function field(label: string, value: string) {
  return (
    <div key={label}>
      <dt>{label}</dt>
      <dd>{value || "Unavailable"}</dd>
    </div>
  );
}
export default function ServerActivity() {
  const [activity, setActivity] = useState<Activity | null>(null);
  const [error, setError] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function update() {
      try {
        const response = await apiFetch("/api/server/activity", {
          signal: controller.signal,
        });
        if (!response.ok) throw new Error("Activity unavailable");
        const next: Activity = await response.json();
        if (!controller.signal.aborted) {
          setActivity(next);
          setError(false);
        }
      } catch {
        if (!controller.signal.aborted) setError(true);
      }
      if (!controller.signal.aborted)
        timer = setTimeout(() => void update(), 3000);
    }
    void update();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, []);
  const slots = activity?.transcodeSlots;
  const activeTitle =
    slots && slots.limit > 0
      ? `Active Streaming Sessions (${slots.active}/${slots.limit})`
      : "Active Streaming Sessions";
  return (
    <>
      <section
        className="admin-card server-activity"
        aria-labelledby="active-streams-title"
      >
        <h2
          id="active-streams-title"
          title="Active transcode resource slots / allowed limit"
        >
          {activeTitle}
        </h2>
        {error && (
          <p role="status" className="profile-field-error">
            Live updates unavailable. Displayed information may be out of date;
            retrying…
          </p>
        )}
        {!activity && !error && <p>Loading activity…</p>}
        {activity?.sessions.length === 0 && (
          <p className="admin-section-hint">No active streaming sessions.</p>
        )}
        {!!activity?.sessions.length && (
          <div className="activity-sessions">
            {activity.sessions.map((s) => (
              <article className="activity-session" key={s.id}>
                <header className="activity-session-heading">
                  <strong>{s.channel}</strong>
                  <span title={s.video}>
                    {clip(s.program || mediaTitle(s.video, true))}
                  </span>
                </header>
                <dl className="activity-details activity-details-primary">
                  {field("User", s.user)}
                  {field("Profile", s.profile)}
                  {field("App Version", s.appVersion?.trim() || "Unknown")}
                  {field("Client Device", s.device)}
                </dl>
                <dl className="activity-details activity-details-secondary">
                  {field(
                    "Playback Time",
                    playbackTime(
                      s.playbackPositionSeconds,
                      s.playbackDurationSeconds,
                    ),
                  )}
                  {field("Client IP", s.sourceIp)}
                  {field(
                    "Bandwidth",
                    s.bandwidthMbps == null
                      ? "Unavailable"
                      : `${s.bandwidthMbps.toFixed(2)} Mbps`,
                  )}
                  {field("Transcoding", s.encoder || "Not Required")}
                </dl>
                <div className="activity-formats">
                  <section>
                    <h3>Source Audio/Video</h3>
                    <p>{audioFormat(s.sourceAudio)}</p>
                    <p>{videoFormat(s.sourceVideo)}</p>
                  </section>
                  <section>
                    <h3>Output Audio/Video</h3>
                    <p>{audioFormat(s.outputAudio)}</p>
                    <p>{videoFormat(s.outputVideo)}</p>
                  </section>
                </div>
              </article>
            ))}
          </div>
        )}
      </section>
      <section
        className="admin-card server-activity"
        aria-labelledby="active-processes-title"
      >
        <h2 id="active-processes-title">Active Background Processes</h2>
        {error && (
          <p className="profile-field-error">
            Live updates unavailable; retrying…
          </p>
        )}
        {!activity && !error && <p>Loading activity…</p>}
        {activity?.processes.length === 0 && (
          <p className="admin-section-hint">No active background processes.</p>
        )}
        {!!activity?.processes.length && (
          <div className="activity-table-scroll">
            <table>
              <thead>
                <tr>
                  <th>Process</th>
                  <th>Runtime</th>
                  <th>Completion</th>
                </tr>
              </thead>
              <tbody>
                {activity.processes.map((p) => (
                  <tr key={p.id}>
                    <td>{p.name}</td>
                    <td>{runtime(p.runtimeSeconds)}</td>
                    <td>
                      <progress
                        max={100}
                        value={p.percent ?? undefined}
                        aria-label={`${p.name} Completion`}
                      />
                      <span title={p.currentFile}>
                        {p.currentFile
                          ? `${p.percent == null ? "In Progress" : `${p.percent.toFixed(0)}%`} - ${clip(p.currentFile)}`
                          : p.percent == null
                            ? `In Progress · ${p.unit}`
                            : `${p.percent.toFixed(0)}% · ${p.completed} / ${p.total} ${p.unit}`}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}
