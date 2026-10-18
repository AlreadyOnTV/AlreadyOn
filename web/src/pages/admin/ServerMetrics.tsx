import { useEffect, useRef, useState, type ReactNode } from 'react';
import { getServerMetrics, type MetricHistory, type MetricSample } from '../../api/client';

type MetricKey = 'cpu' | 'bandwidth' | 'ram' | 'disk';
type Unit = '%' | 'Mbps' | 'MB/s' | 'IOPS' | 'ms';
// Every graph assigns colours in this order: green, yellow, blue, purple, pink, cyan.
const palette = ['#39b5a1', '#f2b84b', '#5289f4', '#ce92ef', '#ef7795', '#6fd3e8'];
// Further cores use the golden angle so their hues stay distinct and stable.
const coreColor = (core: number) => palette[core] ?? `hsl(${Math.round((core * 137.508) % 360)} 70% 62%)`;
const refreshChoices = [1, 5, 10, 30, 60, 120, 300];
const historyChoices = [15, 30, 60, 120, 240, 720];
const format = (value: number | null | undefined, unit: Unit) => {
  if (value == null) return 'Unavailable';
  if (unit === '%') return value.toFixed(1) + '%';
  if (unit === 'MB/s') return value.toFixed(1) + ' MB/s';
  if (unit === 'IOPS') return Math.round(value).toLocaleString() + ' IOPS';
  if (unit === 'ms') return value.toFixed(1) + ' ms';
  return value.toFixed(2) + ' Mbps';
};
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
type TimedValue = { at: number; value: number | null };
function nearestTimedValue(samples: TimedValue[], at: number, maxGap: number) {
  let low = 0;
  let high = samples.length;
  while (low < high) {
    const middle = (low + high) >>> 1;
    if (samples[middle].at < at) low = middle + 1;
    else high = middle;
  }
  const before = samples[low - 1];
  const after = samples[low];
  const nearest = !before ? after : !after ? before : at - before.at <= after.at - at ? before : after;
  return nearest && Math.abs(nearest.at - at) <= maxGap ? nearest.value : null;
}
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
  const [panelSizes, setPanelSizes] = useState<Partial<Record<MetricKey, { width: number; height: number }>>>({});
  useEffect(() => {
    const panels = grid.current?.querySelectorAll<HTMLElement>('.metric-panel');
    if (!panels?.length) return;
    const observer = new ResizeObserver((entries) => {
      setPanelSizes((sizes) => {
        const next = { ...sizes };
        for (const entry of entries) {
          const element = entry.target as HTMLElement;
          const key = element.dataset.metric as MetricKey;
          next[key] = { width: entry.contentRect.width, height: entry.contentRect.height };
        }
        return next;
      });
    });
    for (const panel of panels) observer.observe(panel);
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
      const panel = area.querySelector<HTMLElement>(`[data-metric="${expanded}"]`)!;
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
  const [latencySamples, setLatencySamples] = useState<TimedValue[]>([]);
  useEffect(() => {
    // A tiny uncached same-origin request approximates ping over the existing
    // keep-alive connection; it includes HTTP handling, not just network RTT.
    const controller = new AbortController();
    let pending = false;
    const ping = async () => {
      if (pending) return;
      pending = true;
      const started = performance.now();
      const at = Math.floor(Date.now() / 1000);
      let value: number | null = null;
      try {
        const response = await fetch('/healthz', { cache: 'no-store', signal: AbortSignal.any([controller.signal, AbortSignal.timeout(2000)]) });
        await response.arrayBuffer();
        if (!controller.signal.aborted) value = response.ok ? performance.now() - started : null;
      } catch {
        value = null;
      } finally {
        if (!controller.signal.aborted) {
          setLatency(value);
          setLatencySamples((samples) => [...samples.filter((sample) => sample.at >= at - 720 * 60), { at, value }]);
        }
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
    const panelSize = panelSizes[key] ?? { width: 350, height: key === 'disk' ? 262.5 : 196.875 };
    const mountMetric = (sample: MetricSample | undefined, mount: string) => sample?.diskMounts?.find((item) => item.mount === mount);
    const coreIds = cpu
      ? [...new Set([...(current?.cpuCores ?? []), ...(data?.points ?? []).flatMap((p) => p.cpuCores ?? [])].map((c) => c.core))].sort((a, b) => a - b)
      : [];
    const series: {
      id: string;
      label: string;
      unit: Unit;
      color?: string;
      dash?: string;
      value: (p: MetricSample | undefined) => number | null | undefined;
    }[] = disk
      ? [
          { id: 'configRead', label: 'Config read', unit: 'MB/s', color: palette[0], value: (p) => mountMetric(p, 'config')?.readMBps },
          { id: 'configWrite', label: 'Config write', unit: 'MB/s', color: palette[1], dash: '6 3', value: (p) => mountMetric(p, 'config')?.writeMBps },
          { id: 'mediaRead', label: 'Media read', unit: 'MB/s', color: palette[2], value: (p) => mountMetric(p, 'media')?.readMBps },
          { id: 'mediaWrite', label: 'Media write', unit: 'MB/s', color: palette[3], dash: '6 3', value: (p) => mountMetric(p, 'media')?.writeMBps },
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
    // Older samples must not draw into the axis gutter outside the selected history.
    const points = (data?.points ?? []).filter(p => p.at >= start && p.at <= end).filter(
      (p) => !network || (p.networkInterface === data?.current.networkInterface && p.networkScope === data?.current.networkScope),
    );
    const peak = Math.max(1, ...points.flatMap((point) => series.map((line) => line.value(point) ?? 0)), ...series.map((line) => line.value(current) ?? 0));
    const autoScale = network || disk;
    const ceiling = autoScale ? Math.pow(10, Math.floor(Math.log10(peak))) * Math.ceil(peak / Math.pow(10, Math.floor(Math.log10(peak)))) : 100;
    const width = large ? expandedSize.width - 2 : panelSize.width;
    const height = large ? expandedSize.height - 2 : panelSize.height;
    const plotLeft = 64;
    const right = width - 30;
    const bottom = height - 45;
    const plotTop = disk ? Math.max(panelSize.width < 380 ? 190 : 185, height * 0.62) : Math.max(90, height * 0.25);
    const x = (at: number) => plotLeft + ((at - start) / (end - start)) * (right - plotLeft);
    const y = (value: number) => bottom - (value / ceiling) * (bottom - plotTop);
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
            <span className="metric-help bandwidth-ingress" tabIndex={0} title="Incoming network data rate. Healthy depends on the server's expected traffic and link capacity; sustained saturation can delay playback.">
              Ingress: {format(data?.current.ingressMbps, 'Mbps')}
            </span>
            <span className="metric-help bandwidth-egress" tabIndex={0} title="Outgoing network data rate. Healthy depends on the server's expected traffic and link capacity; sustained saturation can delay playback.">
              Egress: {format(data?.current.egressMbps, 'Mbps')}
            </span>
            <span className="metric-help bandwidth-latency" tabIndex={0} title="Current browser-to-server HTTP round trip to /healthz. Lower and steady is generally better; this includes server handling, not just network time.">
              Connection: {latency == null ? 'Unavailable' : latency < 1 ? '<1ms' : `${Math.round(latency)}ms`}
            </span>
          </div>
        )}
        {cpu && (
          <div className="metric-stats">
            <div>
              <span className="metric-help" tabIndex={0} title="One-minute host runnable-task average. Compare to available CPUs; sustained values above that count can indicate host CPU contention." style={{ color: palette[0] }}>Load: {current?.cpuLoad == null ? 'Unavailable' : current.cpuLoad.toFixed(2)}</span>
              <span className="metric-help" tabIndex={0} title="Share of time tasks were stalled waiting for CPU over the recent window. Near zero is healthy; sustained increases mean CPU contention." style={{ color: palette[1] }}>Pressure: {format(current?.cpuPressure, '%')}</span>
              <span className="metric-help" tabIndex={0} title="Busy share of CPU cores available to the container. Sustained values near 100% leave little headroom." style={{ color: palette[2] }}>Utilization: {whole(current?.cpu)}</span>
            </div>
          </div>
        )}
        {key === 'ram' && (
          <div className="metric-stats">
            <div>
              <span className="metric-help" tabIndex={0} style={{ color: palette[0] }} title="Memory used relative to the container limit (or host RAM when no limit exists). Approaching the limit can trigger reclaim or out-of-memory kills.">
                Usage: {share(current?.memoryUsedBytes, current?.memoryLimitBytes, true)}
              </span>
              <span className="metric-help" tabIndex={0} style={{ color: palette[1] }} title="Percentage of the available memory limit in use. Lower leaves more headroom; sustained near-limit use can cause reclaim or out-of-memory kills.">Utilization: {whole(current?.ram)}</span>
            </div>
          </div>
        )}
        {disk && (
          <div className="disk-metric-table" role="table" aria-label="Disk performance by mount">
            <div className="disk-metric-row disk-metric-header" role="row">
              <span role="columnheader"></span><span role="columnheader">Config</span><span role="columnheader">Media</span>
            </div>
            <div className="disk-metric-row" role="row">
              <span className="metric-help" role="rowheader" tabIndex={0} title="Read and write data rate attributed by the container to each mounted block device. Unavailable for network or unsupported filesystems.">Bandwidth (r/w)</span>
              {(['config', 'media'] as const).map((mount) => {
                const metric = mountMetric(current, mount);
                return <span key={mount} role="cell">{rate(metric?.readMBps)} / {rate(metric?.writeMBps)}</span>;
              })}
            </div>
            <div className="disk-metric-row" role="row">
              <span className="metric-help" role="rowheader" tabIndex={0} title="Read and write I/O operations per second attributed by the container to each mounted block device. No fixed healthy value applies across different hardware and workloads.">IOPS (r/w)</span>
              {(['config', 'media'] as const).map((mount) => {
                const metric = mountMetric(current, mount);
                return <span key={mount} role="cell">{format(metric?.readIOPS, 'IOPS')} / {format(metric?.writeIOPS, 'IOPS')}</span>;
              })}
            </div>
            <div className="disk-metric-row" role="row">
              <span className="metric-help" role="rowheader" tabIndex={0} title="Average block-device request service time for reads and writes. Lower and steady is generally better; sustained increases can indicate contention. Shared devices include other host workloads.">Latency</span>
              {(['config', 'media'] as const).map((mount) => <span key={mount} role="cell">{format(mountMetric(current, mount)?.averageLatencyMs, 'ms')}</span>)}
            </div>
            <div className="disk-metric-row" role="row">
              <span className="metric-help" role="rowheader" tabIndex={0} title="Filesystem space used and total capacity for the config volume or configured media folders. Higher fullness means less free storage; this is not disk performance.">Storage</span>
              <span role="cell">{share(current?.diskUsedBytes, current?.diskTotalBytes)}</span>
              <span role="cell">{share(current?.mediaUsedBytes, current?.mediaTotalBytes)}</span>
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
                ? 'Config and media disk read and write throughput in MB/s over time'
                : title + ' usage history in percent'
          }
          onPointerMove={(event) => {
            if (event.pointerType === 'touch') return;
            const matrix = event.currentTarget.getScreenCTM();
            if (!matrix) return;
            const position = new DOMPoint(event.clientX, event.clientY).matrixTransform(matrix.inverse());
            if (position.x < plotLeft || position.x > right || position.y < plotTop || position.y > bottom) {
              setHover(null);
              return;
            }
            let closest: { at: number; distance: number } | null = null;
            for (const line of series) {
              let previous: { at: number; x: number; y: number } | null = null;
              for (const point of points) {
                const value = line.value(point);
                if (value == null) {
                  previous = null;
                  continue;
                }
                const current = { at: point.at, x: x(point.at), y: y(value) };
                const pointDistance = Math.hypot(position.x - current.x, position.y - current.y);
                if (!closest || pointDistance < closest.distance) closest = { at: point.at, distance: pointDistance };
                if (previous && current.at > previous.at && current.at - previous.at <= interval * 1.5) {
                  const dx = current.x - previous.x;
                  const dy = current.y - previous.y;
                  const lengthSquared = dx * dx + dy * dy;
                  const projection = lengthSquared === 0 ? 0 : Math.max(0, Math.min(1, ((position.x - previous.x) * dx + (position.y - previous.y) * dy) / lengthSquared));
                  const distance = Math.hypot(position.x - (previous.x + projection * dx), position.y - (previous.y + projection * dy));
                  if (distance < (closest?.distance ?? Infinity)) closest = { at: projection < 0.5 ? previous.at : current.at, distance };
                }
                previous = current;
              }
            }
            if (!closest || closest.distance > 6) {
              setHover(null);
              return;
            }
            setHover({ key, large, fraction: (closest.at - start) / (end - start), clientX: event.clientX, clientY: event.clientY });
          }}
          onPointerLeave={() => setHover(null)}
        >
          {[0, 25, 50, 75, 100].map((fraction) => {
            const value = (fraction * ceiling) / 100;
            return (
              <g key={fraction}>
                <line x1={plotLeft} x2={right} y1={y(value)} y2={y(value)} className="metric-gridline" />
                <text x={plotLeft - 16} y={y(value) + 4} textAnchor="end">
                  {autoScale ? value.toLocaleString(undefined, { maximumFractionDigits: 2 }) : value + '%'}
                </text>
              </g>
            );
          })}
          {series.map((line) => {
            let path = '',
              previous: number | null = null;
            const seriesY = y;
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
          <text x={plotLeft} y={height - 19} textAnchor="start">
            {time(start)}
          </text>
          <text x={(plotLeft + right) / 2} y={height - 19} textAnchor="middle">
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
              left: Math.max(8, Math.min(activeHover.clientX + 14, window.innerWidth - 308)),
              top: activeHover.clientY + (disk ? 240 : 170) > window.innerHeight ? Math.max(8, activeHover.clientY - (disk ? 250 : 180)) : activeHover.clientY + 14,
            }}
          >
            <strong>{time(nearest.at)}{network && nearest.networkInterface ? ` · ${nearest.networkInterface}` : ''}</strong>
            {cpu && <>
              <span>Load: {nearest.cpuLoad == null ? 'Unavailable' : nearest.cpuLoad.toFixed(2)}</span>
              <span>Pressure: {format(nearest.cpuPressure, '%')}</span>
              <span>Utilization: {format(nearest.cpu, '%')}</span>
              {coreIds.map((core) => <span key={core}>CPU {core}: {format(nearest.cpuCores?.find((item) => item.core === core)?.utilization, '%')}</span>)}
            </>}
            {key === 'ram' && <>
              <span>Usage: {share(nearest.memoryUsedBytes, nearest.memoryLimitBytes, true)}</span>
              <span>Utilization: {format(nearest.ram, '%')}</span>
            </>}
            {network && <>
              <span>Ingress: {format(nearest.ingressMbps, 'Mbps')}</span>
              <span>Egress: {format(nearest.egressMbps, 'Mbps')}</span>
              <span>Connection: {format(nearestTimedValue(latencySamples, nearest.at, interval), 'ms')}</span>
            </>}
            {disk && (['config', 'media'] as const).map((mount) => {
              const metric = mountMetric(nearest, mount);
              const used = mount === 'config' ? nearest.diskUsedBytes : nearest.mediaUsedBytes;
              const total = mount === 'config' ? nearest.diskTotalBytes : nearest.mediaTotalBytes;
              return <div className="metric-tooltip-volume" key={mount}>
                <strong>{mount === 'config' ? 'Config' : 'Media'}</strong>
                <span>Bandwidth (r/w): {format(metric?.readMBps, 'MB/s')} / {format(metric?.writeMBps, 'MB/s')}</span>
                <span>IOPS (r/w): {format(metric?.readIOPS, 'IOPS')} / {format(metric?.writeIOPS, 'IOPS')}</span>
                <span>Latency: {format(metric?.averageLatencyMs, 'ms')}</span>
                <span>Storage: {share(used, total)}</span>
              </div>;
            })}
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
