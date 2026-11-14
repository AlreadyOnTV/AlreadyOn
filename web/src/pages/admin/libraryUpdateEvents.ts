// Wake the settings progress monitor after a persisted edit, including folder additions/removals.
import type { LibraryUpdateStatus } from '../../api/client';
export const LIBRARY_UPDATE_EVENT = 'alreadyon-library-update';
export function announceLibraryUpdate(status?: LibraryUpdateStatus) {
  window.dispatchEvent(new CustomEvent(LIBRARY_UPDATE_EVENT, { detail: status }));
}
