// Guide time format comes from the current profile rather than server-wide settings.
import { createContext, useContext, type ReactNode } from 'react';
import { useUsers } from '../users/UsersContext';

export type TimeFormat = '12h-padded' | '12h' | '24h';

const DEFAULT_FORMAT: TimeFormat = '12h';

interface GuideSettingsValue {
  timeFormat: TimeFormat;
  setTimeFormat: (format: TimeFormat) => void;
}

const GuideSettingsContext = createContext<GuideSettingsValue>({
  timeFormat: DEFAULT_FORMAT,
  setTimeFormat: () => {},
});

function resolveTimeFormat(stored: string | null): TimeFormat {
  if (stored === '12h-padded' || stored === '12h' || stored === '24h') return stored;
  return DEFAULT_FORMAT;
}

export function GuideSettingsProvider({ children }: { children: ReactNode }) {
  const { currentUser, updateUser } = useUsers();
  const timeFormat = resolveTimeFormat(currentUser.guideTimeFormat);

  // A failed save reloads the server's value, so the UI reverts on its own.
  const setTimeFormat = (format: TimeFormat) => {
    updateUser(currentUser.id, { guideTimeFormat: format }).catch(() => undefined);
  };

  return <GuideSettingsContext.Provider value={{ timeFormat, setTimeFormat }}>{children}</GuideSettingsContext.Provider>;
}

export function useGuideSettings() {
  return useContext(GuideSettingsContext);
}
