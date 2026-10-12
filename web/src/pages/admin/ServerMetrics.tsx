import { useEffect, useRef, useState, type ReactNode } from 'react';
import { getServerMetrics, type MetricHistory, type MetricSample } from '../../api/client';

type MetricKey = 'cpu' | 'bandwidth' | 'ram' | 'disk';
type Unit = '%' | 'Mbps' | 'MB/s';
// Every graph assigns colours in this order: green, yellow, blue, purple, pink, cyan.
const palette = ['#39b5a1', '#f2b84b', '#5289f4', '#ce92ef', '#ef7795', '#6fd3e8'];
// Further cores use the golden angle so their hues stay distinct and stable.
const coreColor = (core: number) => palette[core] ?? `hsl(${Math.round((core * 137.508) % 360)} 70% 62%)`;
const refreshChoices = [1, 5, 10, 30, 60, 120, 300];
const historyChoices = [15, 30, 60, 120, 240, 720];
const format = (value: number | null | undefined, unit: Unit) =>
  value == null ? 'Unavailable' : value.toFixed(unit === '%' ? 1 : 2) + (unit === '%' ? '%' : ' ' + unit);
// Decimal units match how drive capacity is sold and reported by df -H.
function formatBytes(value: number | null | undefined, decimal = false) {
  if (value == null) return 'Unavailable';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let index = 0;
  while (value >= 1000 && index < units.length - 1) {
    value /= 1000;
    index++;
  }
  const digits = value >= 100 || index === 0 ? 0 : 1;
  return (decimal ? value.toFixed(digits) : String(Number(value.toFixed(digits)))) + units[index];
}
const whole = (value: number | null | undefined) => (value == null ? 'Unavailable' : `${Math.round(value)}%`);
const share = (used?: number | null, total?: number | null, decimal = false) =>
  total == null ? 'Unavailable' : `${formatBytes(used, decimal)} / ${formatBytes(total, decimal)}`;
function initial(key: string, fallback: number, choices: number[]) {
  try {
    const value = Number(localStorage.getItem(key));
    return choices.includes(value) ? value : fallback;
  } catch {
    return fallback;
  }
}
export default function ServerMetrics({ actions }: { actions?: ReactNode }) {
  const grid = useRef<HTMLDivElement>(null);
  const dialog = useRef<HTMLDialogElement>(null);
  const [expanded, setExpanded] = useState<MetricKey | null>(null);
  const [hover, setHover] = useState<{ key: MetricKey; large: boolean; fraction: number; clientX: number; clientY: number } | null>(null);
  const [expandedSize, setExpandedSize] = useState({ width: 900, height: 450 });
  const [panelSize, setPanelSize] = useState({ width: 350, height: 320 });
  useEffect(() => {
    const panel = grid.current?.querySelector('.metric-panel');
    if (!panel) return;
    // Draw in screen pixels so axis text matches the HTML legends at any width.
    const observer = new ResizeObserver(([entry]) => {
      setPanelSize({ width: entry.contentRect.width, height: entry.contentRect.height });
    });
    observer.observe(panel);
    return () => observer.disconnect();
  }, []);
  const expandGraph = (key: MetricKey | null) => {
    setHover(null);
    setExpanded(key);
  };
  useEffect(() => {
    if (!expanded || !grid.current || !dialog.current) return;
    const modal = dialog.current;
    const opener = grid.current.querySelector<HTMLElement>(`[data-metric="${expanded}"]`);
    const measure = () => {
      const area = grid.current!;
      const panel = area.querySelector('.metric-panel')!;
      setExpandedSize({
        width: Math.min(area.getBoundingClientRect().width * 0.9, window.innerWidth * 0.95),
        height: Math.min(panel.getBoundingClientRect().height * 1.5, window.innerHeight * 0.9),
      });
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(grid.current);
    window.addEventListener('resize', measure);
    modal.showModal();
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', measure);
      modal.close();
      opener?.focus({ preventScroll: true });
    };
  }, [expanded]);
  const [refresh, setRefresh] = useState(() => initial('metricsRefresh', 1, refreshChoices));
  const [minutes, setMinutes] = useState(() => initial('metricsHistory', 15, historyChoices));
  const [data, setData] = useState<MetricHistory | null>(null);
  const [error, setError] = useState('');
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));
  useEffect(() => {
    const controller = new AbortController();
    let pending = false;
    const load = async () => {
      if (pending) return;
      pending = true;
      try {
        const data = await getServerMetrics(minutes, controller.signal);
        if (!controller.signal.aborted) {
          setData(data);
          setNow(Math.floor(Date.now() / 1000));
          setError('');
        }
      } catch (e) {
        if (!controller.signal.aborted) setError((e as Error).message);
      } finally {
        pending = false;
      }
    };
    void load();
    const timer = window.setInterval(() => void load(), refresh * 1000);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [refresh, minutes]);
  const [latency, setLatency] = useState<number | null>(null);
  useEffect(() => {
    // A tiny uncached same-origin request approximates ping over the existing
    // keep-alive connection; it includes HTTP handling, not just network RTT.
    const controller = new AbortController();
    let pending = false;
    const ping = async () => {
      if (pending) return;
      pending = true;
      const started = performance.now();
      try {
        const response = await fetch('/healthz', { cache: 'no-store', signal: AbortSignal.any([controller.signal, AbortSignal.timeout(2000)]) });
        await response.arrayBuffer();
        if (!controller.signal.aborted) setLatency(response.ok ? performance.now() - started : null);
      } catch {
        if (!controller.signal.aborted) setLatency(null);
      } finally {
        pending = false;
      }
    };
    void ping();
    const timer = window.setInterval(() => void ping(), 1000);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, []);
  const store = (key: string, value: number) => {
    try {
      localStorage.setItem(key, String(value));
    } catch {
      /* Preferences are optional. */
    }
  };
  const end = Math.max(data?.current.at ?? 0, now),
    start = end - minutes * 60;
  const interval = data?.intervalSeconds ?? (minutes === 15 ? 15 : minutes === 30 ? 60 : minutes <= 120 ? 300 : minutes === 240 ? 900 : 3600);
  const averageLabel = interval < 60 ? `${interval} second averages` : `${interval / 60} minute averages`;
  const time = (seconds: number) =>
    new Date(seconds * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', ...(interval < 60 ? { second: '2-digit' as const } : {}) });
  const renderPanel = (key: MetricKey, large = false) => {
    const network = key === 'bandwidth';
    const disk = key === 'disk';
    const cpu = key === 'cpu';
    const title = network ? 'Bandwidth' : disk ? 'Disk' : key.toUpperCase();
    const current = data?.current;
    const coreIds = cpu
      ? [...new Set([...(current?.cpuCores ?? []), ...(data?.points ?? []).flatMap((p) => p.cpuCores ?? [])].map((c) => c.core))].sort((a, b) => a - b)
      : [];
    const series: {
      id: string;
      label: string;
      unit: Unit;
      color?: string;
      dash?: string;
      right?: boolean;
      value: (p: MetricSample | undefined) => number | null | undefined;
    }[] = disk
      ? [
          { id: 'diskRead', label: 'Read', unit: 'MB/s', color: palette[0], value: (p) => p?.diskReadMBps },
          { id: 'diskWrite', label: 'Write', unit: 'MB/s', color: palette[1], dash: '6 3', value: (p) => p?.diskWriteMBps },
          { id: 'ioPressure', label: 'IO Pressure', unit: '%', color: palette[3], right: true, value: (p) => p?.ioPressure },
        ]
      : network
        ? [
            { id: 'ingressMbps', label: 'Ingress', unit: 'Mbps', value: (p) => p?.ingressMbps },
            { id: 'egressMbps', label: 'Egress', unit: 'Mbps', value: (p) => p?.egressMbps },
          ]
        : key === 'ram'
          ? [{ id: 'ram', label: 'Memory Used', unit: '%', value: (p) => p?.ram }]
          : coreIds.length
            ? coreIds.map((core) => ({
                id: 'core-' + core,
                label: 'CPU ' + core,
                unit: '%' as const,
                color: coreColor(core),
                value: (p: MetricSample | undefined) => p?.cpuCores?.find((c) => c.core === core)?.utilization,
              }))
            : [{ id: 'cpu', label: 'CPU', unit: '%', value: (p) => p?.cpu }];
    // Per-core tooltips still lead with the aggregate shown in the title.
    const tooltipRows = coreIds.length ? [{ id: 'cpu', label: 'CPU', unit: '%' as const, value: (p: MetricSample | undefined) => p?.cpu }, ...series] : series;
    const points = (data?.points ?? []).filter(
      (p) => !network || (p.networkInterface === data?.current.networkInterface && p.networkScope === data?.current.networkScope),
    );
    const left = series.filter((line) => !line.right);
    const peak = Math.max(1, ...points.flatMap((p) => left.map((k) => k.value(p) ?? 0)), ...left.map((k) => k.value(current) ?? 0));
    const autoScale = network || disk;
    const ceiling = autoScale ? Math.pow(10, Math.floor(Math.log10(peak))) * Math.ceil(peak / Math.pow(10, Math.floor(Math.log10(peak)))) : 100;
    const width = large ? expandedSize.width - 2 : panelSize.width;
    const height = large ? expandedSize.height - 2 : panelSize.height;
    const right = width - (disk ? 45 : 30);
    const bottom = height - 45;
    const plotTop = disk ? Math.max(115, height * 0.36) : Math.max(90, height * 0.25);
    const x = (at: number) => 48 + ((at - start) / (end - start)) * (right - 48);
    const y = (value: number) => bottom - (value / ceiling) * (bottom - plotTop);
    const percentY = (value: number) => bottom - (value / 100) * (bottom - plotTop);
    const rate = (value: number | null | undefined) => (value == null ? 'Unavailable' : value.toFixed(1) + 'MB/s');
    const activeHover = hover?.key === key && hover.large === large ? hover : null;
    const hoverAt = activeHover ? start + activeHover.fraction * (end - start) : null;
    const available = points.filter((p) => series.some((k) => k.value(p) != null));
    const nearest =
      hoverAt == null
        ? null
        : available.reduce<(typeof available)[number] | null>(
            (best, point) => (!best || Math.abs(point.at - hoverAt) < Math.abs(best.at - hoverAt) ? point : best),
            null,
          );
    return (
      <div
        className={'metric-panel' + (large ? ' metric-panel-expanded' : ' metric-panel-trigger')}
        key={key}
        onPointerMove={(event) => {
          if (event.pointerType === 'touch') return;
          const svg = event.currentTarget.querySelector('svg');
          const matrix = svg?.getScreenCTM();
          if (!matrix) return;
          const position = new DOMPoint(event.clientX, event.clientY).matrixTransform(matrix.inverse());
          setHover({ key, large, fraction: Math.max(0, Math.min(1, (position.x - 48) / (right - 48))), clientX: event.clientX, clientY: event.clientY });
        }}
        onPointerLeave={() => setHover(null)}
        data-metric={key}
        role={large ? undefined : 'button'}
        tabIndex={large ? undefined : 0}
        aria-label={large ? undefined : `Enlarge ${title} graph`}
        aria-haspopup={large ? undefined : 'dialog'}
        onClick={large ? undefined : () => expandGraph(key)}
        onKeyDown={
          large
            ? undefined
            : (event) => {
                if (event.key === 'Enter' || event.key === ' ') {
                  event.preventDefault();
                  expandGraph(key);
                }
              }
        }
      >
        <div className="metric-title">
          <strong>{title}</strong>
          {network && <span>{data?.current.networkInterface}</span>}
        </div>
        {network && (
          <div className="bandwidth-legend">
            <span className="bandwidth-ingress">Ingress: {format(data?.current.ingressMbps, 'Mbps')}</span>
            <span className="bandwidth-egress">Egress: {format(data?.current.egressMbps, 'Mbps')}</span>
            <span className="bandwidth-latency" title="Browser-to-server HTTP round trip to /healthz, measured every second">
              Latency: {latency == null ? 'Unavailable' : latency < 1 ? '<1ms' : `${Math.round(latency)}ms`}
            </span>
          </div>
        )}
        {cpu && (
          <div className="metric-stats">
            <div>
              <span style={{ color: palette[0] }}>Load: {current?.cpuLoad == null ? 'Unavailable' : current.cpuLoad.toFixed(2)}</span>
              <span style={{ color: palette[1] }}>Pressure: {format(current?.cpuPressure, '%')}</span>
              <span style={{ color: palette[2] }}>Utilization: {whole(current?.cpu)}</span>
            </div>
          </div>
        )}
        {key === 'ram' && (
          <div className="metric-stats">
            <div>
              <span
                style={{ color: palette[0] }}
                title={current?.memoryLimited ? 'Container memory limit' : 'No container memory limit; total is host RAM'}
              >
                Usage: {share(current?.memoryUsedBytes, current?.memoryLimitBytes, true)}
              </span>
              <span style={{ color: palette[1] }}>Utilization: {whole(current?.ram)}</span>
            </div>
          </div>
        )}
        {disk && (
          <div className="metric-stats" aria-label="Disk legend">
            <div>
              <span style={{ color: series[0].color }}>Read: {rate(current?.diskReadMBps)}</span>
              <span style={{ color: series[1].color }}>Write: {rate(current?.diskWriteMBps)}</span>
            </div>
            <div>
              <span style={{ color: palette[2] }}>Utilization: {whole(current?.disk)}</span>
              <span style={{ color: series[2].color }}>IO Pressure: {format(current?.ioPressure, '%')}</span>
            </div>
            <div>
              <span style={{ color: palette[4] }} title={current?.diskPath ? `Filesystem holding ${current.diskPath}` : undefined}>
                Config: {share(current?.diskUsedBytes, current?.diskTotalBytes)}
              </span>
              <span style={{ color: palette[5] }} title="Configured media folders; folders sharing a filesystem are counted once">
                Media: {share(current?.mediaUsedBytes, current?.mediaTotalBytes)}
              </span>
            </div>
          </div>
        )}
        <svg
          viewBox={`0 0 ${width} ${height}`}
          role="img"
          aria-label={
            network
              ? 'Ingress and egress bandwidth history in Mbps'
              : disk
                ? 'Disk read and write throughput in MB/s and I/O pressure history in percent'
                : title + ' usage history in percent'
          }
        >
          {[0, 25, 50, 75, 100].map((fraction) => {
            const value = (fraction * ceiling) / 100;
            return (
              <g key={fraction}>
                <line x1="48" x2={right} y1={y(value)} y2={y(value)} className="metric-gridline" />
                <text x="43" y={y(value) + 4} textAnchor="end">
                  {autoScale ? value.toLocaleString(undefined, { maximumFractionDigits: 2 }) : value + '%'}
                </text>
                {disk && (
                  <text x={right + 5} y={y(value) + 4} textAnchor="start">
                    {fraction}%
                  </text>
                )}
              </g>
            );
          })}
          {series.map((line) => {
            let path = '',
              previous: number | null = null;
            const seriesY = line.right ? percentY : y;
            for (const point of points) {
              const value = line.value(point);
              if (value == null) {
                previous = null;
                continue;
              }
              const contiguous = previous !== null && point.at > previous && point.at - previous <= interval * 1.5;
              path += (contiguous ? ' L' : ' M') + x(point.at).toFixed(1) + ' ' + seriesY(value).toFixed(1);
              previous = point.at;
            }
            return (
              <g key={line.id}>
                <path d={path} fill="none" className={'metric-line metric-line-' + line.id} style={{ stroke: line.color, strokeDasharray: line.dash }} />
                {points
                  .filter((p) => line.value(p) != null)
                  .map((p, index) => (
                    <circle
                      key={p.at + ':' + index}
                      cx={x(p.at)}
                      cy={seriesY(line.value(p)!)}
                      r={nearest?.at === p.at ? 5 : 3}
                      className={'metric-dot metric-line-' + line.id}
                      style={{ stroke: line.color }}
                    ></circle>
                  ))}
              </g>
            );
          })}
          {nearest && <line x1={x(nearest.at)} x2={x(nearest.at)} y1={plotTop} y2={bottom} className="metric-hover-line" />}
          <text x="48" y={height - 19} textAnchor="start">
            {time(start)}
          </text>
          <text x={(48 + right) / 2} y={height - 19} textAnchor="middle">
            {time((start + end) / 2)}
          </text>
          <text x={right} y={height - 19} textAnchor="end">
            {time(end)}
          </text>
        </svg>
        {activeHover && nearest && (
          <div
            className="metric-tooltip"
            role="tooltip"
            style={{
              left: Math.max(8, Math.min(activeHover.clientX + 14, window.innerWidth - 228)),
              top: activeHover.clientY + 120 > window.innerHeight ? Math.max(8, activeHover.clientY - 110) : activeHover.clientY + 14,
            }}
          >
            <strong>{time(nearest.at)}</strong>
            {tooltipRows.map((line) => (
              <span key={line.id}>
                {line.label}: {format(line.value(nearest), line.unit)}
              </span>
            ))}
            <span>{averageLabel}</span>
          </div>
        )}
      </div>
    );
  };
  return (
    <section className="server-metrics" aria-label="Server resource metrics">
      <div className="metrics-toolbar">
        {actions && <div className="metrics-actions">{actions}</div>}
        <div className="metrics-controls">
          <label>
            Refresh{' '}
            <select
              aria-label="Metrics refresh interval"
              value={refresh}
              onChange={(e) => {
                const n = Number(e.target.value);
                setRefresh(n);
                store('metricsRefresh', n);
              }}
            >
              {refreshChoices.map((n) => (
                <option key={n} value={n}>
                  {n < 60 ? n + 's' : n / 60 + (n === 60 ? 'min' : 'mins')}
                </option>
              ))}
            </select>
          </label>
          <label>
            History{' '}
            <select
              aria-label="Metrics history"
              value={minutes}
              onChange={(e) => {
                const n = Number(e.target.value);
                setMinutes(n);
                store('metricsHistory', n);
              }}
            >
              {historyChoices.map((n) => (
                <option key={n} value={n}>
                  {n < 60 ? n + 'min' : n / 60 + (n === 60 ? 'hr' : 'hrs')}
                </option>
              ))}
            </select>
          </label>
        </div>
      </div>
      <div className="metrics-grid" ref={grid}>
        {(['cpu', 'ram', 'disk', 'bandwidth'] as const).map((key) => renderPanel(key))}
      </div>
      {expanded && (
        <dialog
          ref={dialog}
          className="metrics-expanded-dialog"
          aria-label={`${expanded === 'bandwidth' ? 'Bandwidth' : expanded === 'disk' ? 'Disk' : expanded.toUpperCase()} graph`}
          style={{ width: expandedSize.width, height: expandedSize.height }}
          onCancel={() => expandGraph(null)}
          onClick={(event) => {
            if (event.target !== event.currentTarget) return;
            const bounds = event.currentTarget.getBoundingClientRect();
            if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) expandGraph(null);
          }}
        >
          {renderPanel(expanded, true)}
          <button type="button" className="admin-modal-close" aria-label="Shrink graph" onClick={() => expandGraph(null)} autoFocus>
            X
          </button>
        </dialog>
      )}

      {error && (
        <p role="alert" className="profile-field-error">
          {error}
        </p>
      )}
    </section>
  );
}
