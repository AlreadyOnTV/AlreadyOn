// Poll owned background work independently of the edit modal so later edits remain available.
import { useEffect, useRef, useState } from 'react';
import { getLibraryUpdates, retryLibraryUpdates, type LibraryUpdateStatus as Status } from '../../api/client';
import { announceLibraryUpdate, LIBRARY_UPDATE_EVENT } from './libraryUpdateEvents';

export default function LibraryUpdateStatus() {
  const [status, setStatus] = useState<Status | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [retrying, setRetrying] = useState(false);
  const retryPending = useRef(false);
  useEffect(() => {
    const controller = new AbortController();
    const poll = { version: 0, timer: undefined as number | undefined };
    const load = () => {
      const version = ++poll.version;
      window.clearTimeout(poll.timer);
      getLibraryUpdates(controller.signal).then((next) => {
        if (controller.signal.aborted || version !== poll.version) return;
        setStatus(next);
        setError(null);
        if (next.state !== 'idle' && next.state !== 'failed') poll.timer = window.setTimeout(load, 1500);
      }).catch((err: Error) => {
        if (controller.signal.aborted || version !== poll.version) return;
        setError(err.message);
        poll.timer = window.setTimeout(load, 3000);
      });
    };
    const changed = (event: Event) => {
      const next = (event as CustomEvent<Status | undefined>).detail;
      if (next) setStatus(next);
      load();
    };
    load();
    window.addEventListener(LIBRARY_UPDATE_EVENT, changed);
    window.addEventListener('focus', load);
    return () => {
      controller.abort();
      window.clearTimeout(poll.timer);
      window.removeEventListener(LIBRARY_UPDATE_EVENT, changed);
      window.removeEventListener('focus', load);
    };
  }, []);
  const retry = async () => {
    if (retryPending.current) return;
    retryPending.current = true;
    setRetrying(true);
    try { announceLibraryUpdate(await retryLibraryUpdates()); setError(null); }
    catch (err) { setError(err instanceof Error ? err.message : 'Could not retry library updates'); }
    finally { retryPending.current = false; setRetrying(false); }
  };
  return <>
    {status && status.revision > 0 && status.state !== 'failed' && (
      <p className="admin-section-hint" role="status" aria-live="polite">
        {status.state === 'idle' ? 'Library changes applied.' : status.state === 'scanning' ? 'Saved—scanning the library in the background…' : status.state === 'updating' ? `Saved—updating channels (${status.processed} of ${status.total})…` : 'Saved—channel updates queued. You can keep making changes.'}
      </p>
    )}
    {status?.state === 'failed' && <div role="alert" className="profile-field-error">
      {status.error} Your preferences are saved.
      <button type="button" className="admin-btn admin-btn-secondary" disabled={retrying} onClick={() => void retry()}>{retrying ? 'Retrying…' : 'Retry Update'}</button>
    </div>}
    {error && <p role="alert" className="profile-field-error">{error}</p>}
  </>;
}
