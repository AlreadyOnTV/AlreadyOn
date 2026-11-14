// Live view of the running server's in-memory log buffer (GET
// /api/logs), the first Admin section backed by a real endpoint instead
// of simulated local state. Polls on an interval rather than a
// websocket/SSE stream — simple, and plenty "live" at this line volume.
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from "react";
import { getLogs, type LogEntry, type LogLevel } from "../../api/client";
import { isBusy, useStartupStatus } from "../../status/useStartupStatus";
import { phaseLabel, progressCounts } from "../../status/phaseText";
import "./ServerLogsSection.css";

const ALL_LEVELS: LogLevel[] = ["debug", "info", "warn", "error", "critical"];
const LINE_LIMIT_OPTIONS = [10, 25, 50, 100, 150, 200, 250];
const DEFAULT_LIMIT = 25;
// Fast while the server is working through the library so its progress
// is visible as it happens; slow when idle, where a change is rare.
const BUSY_POLL_INTERVAL_MS = 1000;
const IDLE_POLL_INTERVAL_MS = 3000;

// Date and time occupy two rows in one narrow column, leaving log messages room.
function formatTime(iso: string, timeZone: string): { date: string; time: string } {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return { date: iso, time: '' };
  let parts: Record<string, string>;
  try {
    parts = Object.fromEntries(
      new Intl.DateTimeFormat("en-US", { timeZone, month: "2-digit", day: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: true })
        .formatToParts(d)
        .map((p) => [p.type, p.value]),
    );
  } catch {
    return { date: iso, time: '' };
  }
  return { date: `${parts.month}/${parts.day}/${parts.year}`, time: `${parts.hour}:${parts.minute}:${parts.second} ${parts.dayPeriod}` };
}

export default function ServerLogsSection() {
  const viewer = useRef<HTMLDivElement>(null);
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [timeZone, setTimeZone] = useState("America/New_York");
  const [levels, setLevels] = useState<Set<LogLevel>>(new Set(ALL_LEVELS));
  const [limit, setLimit] = useState(DEFAULT_LIMIT);
  const [paused, setPaused] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  // Entries at or before this time (ms) are hidden. Only this page forgets
  // them: the server keeps its log, and a reload starts from scratch.
  const [clearedThrough, setClearedThrough] = useState<number | null>(null);
  const { status, recheck } = useStartupStatus();
  const busy = status !== null && isBusy(status);

  const levelsKey = ALL_LEVELS.filter((l) => levels.has(l)).join(",");

  useEffect(() => {
    if (paused) return;

    const controller = new AbortController();
    const poll = () => {
      getLogs(levelsKey ? (levelsKey.split(",") as LogLevel[]) : [], limit)
        .then((data) => {
          if (!controller.signal.aborted) {
            setEntries(data.entries);
            if (data.timeZone) setTimeZone(data.timeZone);
            setError(null);
          }
        })
        .catch((err: Error) => {
          if (!controller.signal.aborted) setError(err.message);
        });
      // While idle this is how a later refresh gets noticed and sped up.
      if (!busy) recheck();
    };

    poll();
    const id = window.setInterval(poll, busy ? BUSY_POLL_INTERVAL_MS : IDLE_POLL_INTERVAL_MS);
    return () => {
      controller.abort();
      window.clearInterval(id);
    };
  }, [paused, limit, levelsKey, busy, recheck]);

  const toggleLevel = (level: LogLevel) => {
    setLevels((prev) => {
      const next = new Set(prev);
      if (next.has(level)) {
        next.delete(level);
      } else {
        next.add(level);
      }
      return next;
    });
  };

  const visibleEntries = clearedThrough === null ? entries : entries.filter((e) => !(Date.parse(e.time) <= clearedThrough));

  useLayoutEffect(() => {
    const element = viewer.current;
    if (paused || !element) return;
    const follow = () => { element.scrollTop = element.scrollHeight; };
    follow();
    // Fonts, wrapped lines and the startup hint can change the viewport after
    // the entries commit. Keep following those layout changes as well.
    const observer = new ResizeObserver(follow);
    observer.observe(element);
    if (element.firstElementChild) observer.observe(element.firstElementChild);
    return () => observer.disconnect();
  }, [entries, paused, clearedThrough, limit, levelsKey]);

  const handleClear = () => {
    const newest = Date.parse(visibleEntries[visibleEntries.length - 1].time);
    if (!Number.isNaN(newest)) setClearedThrough(newest);
  };

  const handleCopy = async () => {
    const text = visibleEntries.map((e) => `${e.time} [${e.level.toUpperCase()}] ${e.message}`).join("\n");
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setError("Couldn't copy to clipboard");
    }
  };

  return (
    <div className="admin-card log-card">
      <h3>Server Logs</h3>

      <div className="log-viewer-controls">
        <div className="log-viewer-levels">
          {ALL_LEVELS.map((level) => (
            <button
              key={level}
              type="button"
              className={"log-level-chip" + (levels.has(level) ? " log-level-chip-active" : "")}
              onClick={() => toggleLevel(level)}
            >
              {level}
            </button>
          ))}
        </div>
        <div className="log-viewer-actions">
          <select value={limit} onChange={(e) => setLimit(Number(e.target.value))} aria-label="Lines To Display">
            {LINE_LIMIT_OPTIONS.map((n) => (
              <option key={n} value={n}>
                {n} lines
              </option>
            ))}
          </select>
          <button className="admin-btn admin-btn-secondary" onClick={() => setPaused((p) => !p)}>
            {paused ? "Resume" : "Pause"}
          </button>
          <button className="admin-btn admin-btn-secondary" onClick={handleCopy} disabled={visibleEntries.length === 0}>
            {copied ? "Copied!" : "Copy"}
          </button>
          <button className="admin-btn admin-btn-secondary" onClick={handleClear} disabled={visibleEntries.length === 0}>
            Clear
          </button>
        </div>
      </div>

      {busy && status && (
        <p className="log-viewer-busy-hint" role="status">
          <strong>The server is working, it isn't stuck.</strong> {phaseLabel(status)}
          {progressCounts(status) && <> — {progressCounts(status)}</>}. The first run can take a few minutes on a large library; new log lines appear here as each step happens.
        </p>
      )}

      {error && <p className="profile-field-error">{error}</p>}

      <div ref={viewer} className="log-viewer" style={{ "--log-lines": limit } as CSSProperties}>
        <div className="log-viewer-content">
          {visibleEntries.length === 0 ? (
            <p className="log-viewer-empty">
              {error ? "Log unavailable." : "No log entries" + (levels.size < ALL_LEVELS.length ? " match the selected levels." : ".")}
            </p>
          ) : (
            visibleEntries.map((e, i) => {
              const timestamp = formatTime(e.time, timeZone);
              return (
                <div key={e.time + i} className={"log-line log-level-" + e.level}>
                  <span className="log-line-time"><span>{timestamp.date}</span><span>{timestamp.time}</span></span>
                  <span className={"log-line-level-badge log-level-badge-" + e.level}>{e.level}</span>
                  <span className="log-line-message">{e.message}</span>
                </div>
              );
            })
          )}
        </div>
      </div>

      {paused && <p className="log-viewer-paused-hint">Paused — new log entries aren't being fetched.</p>}
    </div>
  );
}
