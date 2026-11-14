// Guide time format comes from the current profile rather than server-wide settings.
import { createContext, useContext } from 'react';

export type TimeFormat = '12h-padded' | '12h' | '24h';

const DEFAULT_FORMAT: TimeFormat = '12h';

interface GuideSettingsValue {
  timeFormat: TimeFormat;
  setTimeFormat: (format: TimeFormat) => void;
}

export const GuideSettingsContext = createContext<GuideSettingsValue>({
  timeFormat: DEFAULT_FORMAT,
  setTimeFormat: () => {},
});

export function resolveTimeFormat(stored: string | null): TimeFormat {
  if (stored === '12h-padded' || stored === '12h' || stored === '24h') return stored;
  return DEFAULT_FORMAT;
}


export function useGuideSettings() {
  return useContext(GuideSettingsContext);
}
