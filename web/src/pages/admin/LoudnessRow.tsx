import { useEffect, useRef, useState } from 'react';
import { getLoudness, setLoudness, runLoudness, type LoudnessStatus } from '../../api/client';
import SettingToggle from './SettingToggle';
import InfoModal from './InfoModal';

// Host pressure and recovery are distinct; a paused pool must not display None.
const LIMITS: Record<string, string> = { cpu: 'CPU Utilization', ram: 'RAM Utilization', disk: 'Disk Utilization', network: 'Network Utilization', swap: 'Paging / Swap Activity', playback: 'Foreground HDR Playback', recovery: 'Waiting for Quiet Host', metrics: 'Waiting for Host Metrics' };

const shorten = (name: string, max: number) => (name.length > max ? name.slice(0, max) + '…' : name);

function currentFile(status: LoudnessStatus): string {
  if (!status.enabled) return 'Disabled';
  if (!status.running) return status.pending ? 'Waiting' : 'Up To Date';
  return shorten(status.currentFile, 50) + (status.active > 1 ? ` (+${status.active - 1} more)` : '');
}

export default function LoudnessRow() {
  const [status, setStatus] = useState<LoudnessStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [info, setInfo] = useState(false);
  const revision = useRef(0);
  useEffect(() => {
    const poll = { cancelled: false, pending: false };
    const load = async () => {
      if (poll.pending) return;
      poll.pending = true;
      const current = revision.current;
      try {
        const next = await getLoudness();
        if (!poll.cancelled && current === revision.current) {
          setStatus(next);
          setError('');
        }
      } catch (e) {
        if (!poll.cancelled && current === revision.current) setError((e as Error).message);
      } finally {
        poll.pending = false;
      }
    };
    void load();
    const timer = window.setInterval(() => void load(), 3000);
    return () => {
      poll.cancelled = true;
      window.clearInterval(timer);
    };
  }, []);
  const update = async (action: () => Promise<LoudnessStatus>) => {
    revision.current++;
    setBusy(true);
    setError('');
    try {
      setStatus(await action());
    } catch (e) {
      setError((e as Error).message);
    } finally {
      revision.current++;
      setBusy(false);
    }
  };
  return (
    <>
      <div className="setting-row">
        <div className="setting-name">Loudness Detection</div>
        <div className="setting-control">
          <fieldset disabled={busy || status === null} style={{ border: 0, padding: 0, margin: 0 }}>
            <SettingToggle label="Loudness Detection" checked={status?.enabled ?? false} onChange={(enabled) => void update(() => setLoudness(enabled))} text />
          </fieldset>
        </div>
        <div className="setting-actions">
          <button
            type="button"
            className="admin-btn admin-btn-secondary"
            disabled={busy || !status?.enabled || status.running}
            onClick={() => void update(runLoudness)}
          >
            Run Now
          </button>
          <button type="button" className="admin-btn admin-btn-secondary" onClick={() => setInfo(true)}>
            More Info
          </button>
        </div>
        {status && (
          <dl className="setting-note loudness-status" role="status">
            <dt>Current File:</dt>
            <dd>{currentFile(status)}</dd>
            <dt>Workers:</dt>
            <dd>{status.running ? status.workers : 0}</dd>
            <dt>Processed:</dt>
            <dd>{status.ready}</dd>
            <dt>Pending:</dt>
            <dd>{status.pending}</dd>
            <dt>Skipped:</dt>
            <dd>{status.skipped}</dd>
            <dt>Failed:</dt>
            <dd>{status.failed}</dd>
            <dt>Limited By:</dt>
            <dd>{status.running ? LIMITS[status.limitedBy] ?? 'None' : 'None'}</dd>
            {status.failed > 0 && status.lastError && (
              <>
                <dt>Last Error:</dt>
                <dd>{status.lastError}</dd>
              </>
            )}
          </dl>
        )}
        {error && (
          <p className="setting-note profile-field-error" role="alert">
            {error}
          </p>
        )}
      </div>
      <InfoModal open={info} title="Loudness Detection" onClose={() => setInfo(false)}>
        <p>
          Off by default. Enabling this scans the playback audio track of every episode, movie and commercial, using parallel background workers with low CPU and disk priority. Background work targets at most 80% host CPU utilization while reserving one logical CPU’s capacity on multicore systems, yields above 80% RAM usage or when swapping, and pauses at 80% disk utilization. Background HTTP transfers share a 750 Mbps limit; network-mounted media requires host network shaping for a strict bandwidth cap. Work resumes after two clear one-second samples.
          The first library scan can take a long time.
        </p>
        <p>
          Full-track measurements are stored in the database. New or changed files are measured automatically; Run Now checks for outstanding files and retries
          failed measurements. Silent files and videos without audio are skipped.
        </p>
        <p>
          During playback, measured audio is normalized on the server to -16 LUFS with a -1.5 dB true-peak ceiling, then encoded as stereo AAC. Compatible video
          is copied when the existing playback and seeking rules permit. No source files are modified and no normalized audio copies are saved.
        </p>
        <p>
          This uses extra CPU and a streaming worker, and may add startup time to videos that previously played directly. Joining a scheduled HLS stream
          mid-file can also require video conversion for accurate timing. Files awaiting analysis play at their original volume. Disabling cancels background
          analysis; playback changes take effect on the next tune or program transition. Measurements are retained for reuse.
        </p>
      </InfoModal>
    </>
  );
}
