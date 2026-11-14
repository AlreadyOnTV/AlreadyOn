// Persist the active profile's time format while keeping context exports refresh-safe.
import type { ReactNode } from 'react';
import { useUsers } from '../users/UsersContext';
import { GuideSettingsContext, resolveTimeFormat, type TimeFormat } from './GuideSettingsContext';

export function GuideSettingsProvider({ children }: { children: ReactNode }) {
  const { currentUser, updateUser } = useUsers();
  const timeFormat = resolveTimeFormat(currentUser.guideTimeFormat);

  // A failed save reloads the server's value, so the UI reverts on its own.
  const setTimeFormat = (format: TimeFormat) => {
    updateUser(currentUser.id, { guideTimeFormat: format }).catch(() => undefined);
  };

  return <GuideSettingsContext.Provider value={{ timeFormat, setTimeFormat }}>{children}</GuideSettingsContext.Provider>;
}
