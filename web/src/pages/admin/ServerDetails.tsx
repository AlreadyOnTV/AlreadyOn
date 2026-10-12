// Build identity is server-owned; uptime refreshes while this card is visible.
import { useEffect, useState, type ReactNode } from 'react';
import { apiFetch } from '../../api/access';

type Details = { uptimeSeconds: number; ipAddresses: string[]; appVersion: string; timeZone: string; ffmpegVersion: string };
function uptime(seconds: number) {
  const n = Math.floor(seconds);
  return `${Math.floor(n / 86400)}d ${Math.floor(n % 86400 / 3600)}h ${Math.floor(n % 3600 / 60)}m ${n % 60}s`;
}
export default function ServerDetails({ actions }: { actions?: ReactNode }) {
  const [details, setDetails] = useState<Details | null>(null);
  const [error, setError] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function update() {
      try {
        const response = await apiFetch('/api/server/details', { signal: controller.signal });
        if (!response.ok) throw new Error('Details unavailable');
        const next: Details = await response.json();
        if (!controller.signal.aborted) { setDetails(next); setError(false); }
      } catch { if (!controller.signal.aborted) setError(true); }
      if (!controller.signal.aborted) timer = setTimeout(() => void update(), 1000);
    }
    void update();
    return () => { controller.abort(); clearTimeout(timer); };
  }, []);
  return <>
    <div className="folder-card-header"><h3>Server Details</h3>{actions}</div>
    {error && <p role="status" className="profile-field-error">Server details unavailable. Retrying…</p>}
    {!details && !error && <p>Loading server details…</p>}
    {details && <dl className="server-build-details">
      <div><dt>Uptime</dt><dd>{uptime(details.uptimeSeconds)}</dd></div>
      <div><dt>IP Address</dt><dd>{details.ipAddresses.join(', ') || 'Unavailable (host address cannot be detected)'}</dd></div>
      <div><dt>Server Version</dt><dd>{details.appVersion}</dd></div>
      <div><dt>Time Zone</dt><dd>{details.timeZone}</dd></div>
      <div><dt>FFmpeg Version</dt><dd>{details.ffmpegVersion}</dd></div>
    </dl>}
  </>;
}
