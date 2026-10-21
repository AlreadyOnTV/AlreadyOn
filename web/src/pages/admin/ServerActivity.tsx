import { useEffect, useState } from 'react';
import { apiFetch } from '../../api/access';
import { mediaTitle } from '../mediaTitle';
import './ServerActivity.css';

type Session = {
  id: string;
  channel: string;
  video: string;
  profile: string;
  sourceIp: string;
  device: string;
  mode: string;
  encoder: string;
  videoReason?: string;
  audioReason?: string;
  bandwidthMbps: number | null;
  appVersion?: string;
  commercial: boolean;
  paused: boolean;
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
type Activity = { sessions: Session[]; processes: Process[] };
// Explicit filenames retain their release tags; the visible cap includes the ellipsis.
const clip = (value: string) => {
  const chars = Array.from(value);
  return chars.length > 50 ? chars.slice(0, 49).join('') + '…' : value;
};
function runtime(seconds: number) {
  const n = Math.floor(seconds);
  return n >= 3600 ? `${Math.floor(n / 3600)}h ${Math.floor((n % 3600) / 60)}m ${n % 60}s` : `${Math.floor(n / 60)}m ${n % 60}s`;
}
export default function ServerActivity() {
  const [activity, setActivity] = useState<Activity | null>(null);
  const [error, setError] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function update() {
      try {
        const response = await apiFetch('/api/server/activity', { signal: controller.signal });
        if (!response.ok) throw new Error('Activity unavailable');
        const next: Activity = await response.json();
        if (!controller.signal.aborted) {
          setActivity(next);
          setError(false);
        }
      } catch {
        if (!controller.signal.aborted) setError(true);
      }
      if (!controller.signal.aborted) timer = setTimeout(() => void update(), 3000);
    }
    void update();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, []);
  return (
    <>
      <section className="admin-card server-activity" aria-labelledby="active-streams-title">
        <h2 id="active-streams-title">Active Streaming Sessions</h2>
        {error && (
          <p role="status" className="profile-field-error">
            Live updates unavailable. Displayed information may be out of date; retrying…
          </p>
        )}
        {!activity && !error && <p>Loading activity…</p>}
        {activity?.sessions.length === 0 && <p className="admin-section-hint">No active streaming sessions.</p>}
        {!!activity?.sessions.length && (
          <>
            <div className="activity-sessions">
              {activity.sessions.map((s) => (
                <article className="activity-session" key={s.id}>
                  <header>
                    <strong>{s.channel}</strong>
                    <span title={mediaTitle(s.video, true)}>{clip(mediaTitle(s.video, true))}</span>
                  </header>
                  <dl className="activity-details">
                    <div>
                      <dt>Profile</dt>
                      <dd>{s.profile}</dd>
                    </div>
                    <div>
                      <dt>Source IP</dt>
                      <dd>{s.sourceIp}</dd>
                    </div>
                    <div>
                      <dt>Client Device</dt>
                      <dd>{s.device}</dd>
                    </div>
                    <div>
                      <dt>Status</dt>
                      <dd>{s.commercial ? 'Commercial Break — Updates Paused' : s.paused ? 'Paused' : 'Playing'}</dd>
                    </div>
                    <div>
                      <dt>Bandwidth</dt>
                      <dd>{s.bandwidthMbps == null ? 'Unavailable' : `${s.bandwidthMbps.toFixed(2)} Mbps`}</dd>
                    </div>
                    <div>
                      <dt>App Version</dt>
                      <dd>{s.appVersion?.trim() || 'Unknown'}</dd>
                    </div>
                    <div>
                      <dt>Playback</dt>
                      <dd>{s.mode === 'direct' ? 'Direct Play' : s.mode === 'remux' ? 'Remux' : 'Transcode'}</dd>
                    </div>
                    <div>
                      <dt>Transcoding</dt>
                      <dd>{s.encoder || 'Not Required'}</dd>
                    </div>
                  </dl>
                  <div className="activity-reasons">
                    <p>
                      <strong>Video:</strong> {s.videoReason || 'Playback details unavailable.'}
                    </p>
                    <p>
                      <strong>Audio:</strong> {s.audioReason || 'Playback details unavailable.'}
                    </p>
                  </div>
                </article>
              ))}
            </div>
          </>
        )}
      </section>
      <section className="admin-card server-activity" aria-labelledby="active-processes-title">
        <h2 id="active-processes-title">Active Background Processes</h2>
        {error && <p className="profile-field-error">Live updates unavailable; retrying…</p>}
        {!activity && !error && <p>Loading activity…</p>}
        {activity?.processes.length === 0 && <p className="admin-section-hint">No active background processes.</p>}
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
                      <progress max={100} value={p.percent ?? undefined} aria-label={`${p.name} Completion`} />
                      <span title={p.currentFile}>
                        {p.currentFile
                          ? `${p.percent == null ? 'In Progress' : `${p.percent.toFixed(0)}%`} - ${clip(p.currentFile)}`
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
