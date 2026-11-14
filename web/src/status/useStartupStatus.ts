import { useCallback, useEffect, useRef, useState } from 'react';
import { getStartupStatus, type StartupStatus } from '../api/client';

const POLL_INTERVAL_MS = 1500;
const MAX_CONSECUTIVE_FAILURES = 3;

// The server is still working (or hasn't finished its very first run).
export function isBusy(status: StartupStatus): boolean {
  return status.active || !status.startupComplete;
}

// Polls /api/status/startup only while the server is busy, then stops.
// `completions` increments each time a busy run is seen to finish, so a
// caller can reload its own data (the guide) exactly when new content
// may have landed. A page opened after startup already finished makes
// one request and never polls. `recheck` is a one-off status check for
// pages that stay open while idle: if the server has become busy since
// (a refresh, a content poll), polling resumes on its own.
export function useStartupStatus(): { status: StartupStatus | null; completions: number; recheck: () => void } {
  const [status, setStatus] = useState<StartupStatus | null>(null);
  const [completions, setCompletions] = useState(0);
  const tickRef = useRef<(() => Promise<void>) | null>(null);

  useEffect(() => {
    // Each setup owns its poller; cleanup cannot cancel the replacement run.
    const poll = { cancelled: false, inFlight: false, timer: undefined as number | undefined, wasBusy: false, failures: 0 };

    const tick = async () => {
      if (poll.inFlight) return;
      poll.inFlight = true;
      window.clearTimeout(poll.timer);
      try {
        const next = await getStartupStatus();
        if (poll.cancelled) return;
        poll.failures = 0;
        setStatus(next);
        const busy = isBusy(next);
        if (poll.wasBusy && !busy) setCompletions((n) => n + 1);
        poll.wasBusy = busy;
        if (busy) poll.timer = window.setTimeout(tick, POLL_INTERVAL_MS);
      } catch {
        if (poll.cancelled) return;
        // A server that's restarting mid-run, or an older server without
        // this endpoint: give up quietly rather than retry forever.
        poll.failures += 1;
        if (poll.failures < MAX_CONSECUTIVE_FAILURES) poll.timer = window.setTimeout(tick, POLL_INTERVAL_MS * 2);
      } finally {
        poll.inFlight = false;
      }
    };

    tickRef.current = tick;
    void tick();
    return () => {
      poll.cancelled = true;
      tickRef.current = null;
      window.clearTimeout(poll.timer);
    };
  }, []);

  const recheck = useCallback(() => {
    void tickRef.current?.();
  }, []);

  return { status, completions, recheck };
}
