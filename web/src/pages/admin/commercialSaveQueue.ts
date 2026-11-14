// Coalesce unsent commercial snapshots and serialize requests. Responses never
// replace the form's newer local draft; a failed latest snapshot can be retried.
import type { CommercialSettings } from '../../api/client';

export type CommercialSaveState = 'saving' | 'saved' | 'error';
export function commercialSaveQueue(
  save: (settings: CommercialSettings) => Promise<unknown>,
  report: (state: CommercialSaveState, error?: string) => void,
) {
  let pending: CommercialSettings | null = null;
  let latest: CommercialSettings | null = null;
  let running: Promise<void> | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const flush = (): Promise<void> => {
    clearTimeout(timer);
    if (running) return running;
    running = (async () => {
      while (pending) {
        const snapshot = pending;
        pending = null;
        report('saving');
        try {
          await save(snapshot);
          if (!pending) report('saved');
        } catch (error) {
          // A newer full snapshot carries all earlier edits, including the failed one.
          if (!pending) report('error', error instanceof Error ? error.message : 'Could not save commercial settings');
        }
      }
    })().finally(() => { running = null; if (!pending) clearTimeout(timer); });
    return running;
  };
  const enqueue = (settings: CommercialSettings) => {
    latest = { ...settings };
    pending = latest;
    report('saving');
    clearTimeout(timer);
    timer = setTimeout(() => { void flush(); }, 300);
  };
  return {
    enqueue,
    flush,
    retry: () => { if (latest) { enqueue(latest); return flush(); } return Promise.resolve(); },
  };
}
