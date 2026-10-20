import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import type { StartupStatus } from "../api/client";
import { isBusy } from "./useStartupStatus";
import { phaseLabel, progressCounts } from "./phaseText";
import "./StartupNotice.css";

type Notice =
  | { kind: "running"; title: string; phaseLabel: string; counts: string }
  | { kind: "failed"; title: string; detail: string }
  | { kind: "noRoots"; title: string; detail: string };

// What (if anything) the user should be told about the server's
// background library work. Null means nothing worth showing.
function describeStartup(status: StartupStatus | null): Notice | null {
  if (!status) return null;
  if (isBusy(status)) {
    return {
      kind: "running",
      title:
        status.trigger === "startup" || !status.startupComplete
          ? "Tuning In…"
          : "Refreshing Your Library…",
      phaseLabel: phaseLabel(status),
      counts: progressCounts(status),
    };
  }
  if (status.phase === "failed") {
    return {
      kind: "failed",
      title: "Something Went Wrong While Loading Your Library",
      detail: status.lastError || "The last library update reported an error.",
    };
  }
  if (status.mediaRoots === 0) {
    return {
      kind: "noRoots",
      title: "No Media Folders Are Set Up",
      detail:
        "None of the configured TV or movie folders could be read. Check them on Library Paths.",
    };
  }
  return null;
}

// "panel" fills the guide area on first run; "banner" is the compact
// strip at the top of every admin page.
export default function StartupNotice({
  status,
  variant,
}: {
  status: StartupStatus | null;
  variant: "panel" | "banner";
}) {
  const notice = describeStartup(status);
  const running = notice?.kind === "running";
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!running) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [running]);
  // Use the server's cycle start so navigation/reloads do not reset the clock.
  const started = status?.startedAt ? Date.parse(status.startedAt) : NaN;
  const seconds = Math.max(0, Math.floor((now - started) / 1000));
  const elapsed = Number.isFinite(seconds)
    ? `${seconds >= 3600 ? `${Math.floor(seconds / 3600)}h ` : ""}${Math.floor((seconds % 3600) / 60)}m ${seconds % 60}s`
    : null;
  if (!notice) return null;

  return (
    <div
      className={`startup-notice startup-notice-${variant} startup-notice-${notice.kind}`}
      role="status"
      aria-live="polite"
    >
      {notice.kind === "running" && (
        <span className="startup-spinner" aria-hidden="true" />
      )}
      <div className="startup-notice-text">
        <div className="startup-notice-title">{notice.title}</div>
        {notice.kind === "running" ? (
          <>
            <div className="startup-notice-detail">
              {notice.phaseLabel}
              {notice.counts && <> — {notice.counts}</>}
            </div>
            {elapsed !== null && (
              <div className="startup-notice-hint" aria-live="off">
                Running for {elapsed}
              </div>
            )}
            {variant === "panel" && (
              <div className="startup-notice-hint">
                The first run can take a few minutes for a large library.{" "}
                <Link to="/admin/logs">Server Logs</Link> show each step.
              </div>
            )}
          </>
        ) : (
          <>
            <div className="startup-notice-detail">{notice.detail}</div>
            <div className="startup-notice-hint">
              {notice.kind === "noRoots" && (
                <>
                  <Link to="/admin/library-paths">Open Library Paths</Link> to
                  add your folders, or{" "}
                </>
              )}
              <Link to="/admin/logs">
                {notice.kind === "noRoots" ? "see" : "Open"} Server Logs
              </Link>{" "}
              for details.
            </div>
          </>
        )}
      </div>
    </div>
  );
}
