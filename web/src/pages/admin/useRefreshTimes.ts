import { useCallback, useEffect, useState } from 'react';
import { getRefreshTimes, type RefreshTimes } from '../../api/client';

// When the library was last scanned and the schedules last regenerated; call
// reload after a manual refresh to show the new time.
export function useRefreshTimes() {
  const [times, setTimes] = useState<RefreshTimes | null>(null);
  const reload = useCallback(() => {
    getRefreshTimes().then(setTimes, () => undefined);
  }, []);
  useEffect(() => {
    reload();
  }, [reload]);
  return { times, reload };
}
