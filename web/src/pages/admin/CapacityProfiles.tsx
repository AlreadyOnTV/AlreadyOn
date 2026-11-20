import { useEffect, useState } from 'react';
import { apiFetch } from '../../api/access';

type CapacityProfile = {
  sourceClass: string; encoder: string; hardwareDecode: boolean; toneMapper?: string;
  height: number; fps: number; threads: number; validatedStreams: number; verified: boolean;
};
const names: Record<string, string> = {
  'h264-sdr-1080p': '1080p H.264 SDR', 'hevc-sdr-1080p': '1080p HEVC SDR',
  'hevc-main10-4k-pq': '4K HDR10 / PQ', 'hevc-main10-4k-hlg': '4K HLG', 'h264-sdr-480p': 'SD H.264',
};
// These are measured per-pipeline concurrent limits. A fast single stream does
// not establish the number of simultaneous transcodes the server can sustain.
export default function CapacityProfiles() {
  const [profiles, setProfiles] = useState<CapacityProfile[]>([]);
  useEffect(() => {
    const controller = new AbortController();
    const read = async () => {
      try {
        const response = await apiFetch('/api/settings/transcoding/capacity', { signal: controller.signal });
        if (!response.ok) return;
        const result = await response.json();
        if (!controller.signal.aborted) setProfiles(Array.isArray(result.profiles) ? result.profiles : []);
      } catch { /* Preserve the last snapshot during server restarts. */ }
    };
    void read();
    const timer = window.setInterval(() => void read(), 5000);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, []);
  return <div className="admin-card">
    <h3>Validated Transcoding Capacity</h3>
    <p>Limits depend on the source and processing path. Unverified pipelines use conservative admission limits.</p>
    {profiles.length === 0 ? <p>No validated pipeline results yet.</p> :
      <div className="admin-table-scroll"><table className="admin-table">
        <thead><tr><th>Source</th><th>Processing</th><th>Output</th><th>Threads per stream</th><th>Validated streams</th></tr></thead>
        <tbody>{profiles.map((p, i) => <tr key={`${p.sourceClass}-${p.hardwareDecode}-${i}`}>
          <td>{names[p.sourceClass] ?? p.sourceClass}</td>
          <td>{p.hardwareDecode ? 'GPU decode' : 'CPU decode'} · {p.encoder || 'CPU'} encode{p.toneMapper ? ` · ${p.toneMapper === 'gpu' ? 'GPU' : 'CPU'} tone mapping` : ''}</td>
          <td>{p.height}p{p.fps}</td><td>{p.threads}</td>
          <td>{!p.verified ? 'Unverified' : p.validatedStreams > 0 ? p.validatedStreams : 'Below real time'}</td>
        </tr>)}</tbody>
      </table></div>}
  </div>;
}
