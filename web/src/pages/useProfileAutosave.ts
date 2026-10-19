// Serialize profile writes because each response contains the whole profile.
// Coalesce queued edits and compare with confirmed values, never optimistic state.
import { useCallback, useEffect, useRef, useState } from 'react';

export type ProfileSetting = 'name' | 'icon' | 'pin' | 'guideTimeFormat' | 'audioLanguage' | 'subtitleLanguage' | 'subtitleMode';
type Values = Partial<Record<ProfileSetting, string | null>>;

export function useProfileAutosave(values: Values) {
  const confirmed = useRef({ ...values });
  const previousValues = useRef(values);
  const revisions = useRef<Partial<Record<ProfileSetting, number>>>({});
  const pending = useRef<Partial<Record<ProfileSetting, number>>>({});
  const queue = useRef(Promise.resolve());
  const desired = useRef<Values>({});
  const requests = useRef<Partial<Record<ProfileSetting, Promise<boolean>>>>({});
  const mounted = useRef(true);
  const timers = useRef<Partial<Record<ProfileSetting, number>>>({});
  const [saved, setSaved] = useState<Partial<Record<ProfileSetting, number>>>({});
  const [errors, setErrors] = useState<Partial<Record<ProfileSetting, string>>>({});

  useEffect(() => {
    // Remote refreshes update the baseline only for settings without local writes.
    for (const key of Object.keys(values) as ProfileSetting[]) {
      if (!pending.current[key] && previousValues.current[key] !== values[key]) confirmed.current[key] = values[key];
    }
    previousValues.current = values;
  }, [values]);

  useEffect(() => {
    mounted.current = true;
    const activeTimers = timers.current;
    return () => {
      mounted.current = false;
      Object.values(activeTimers).forEach(window.clearTimeout);
    };
  }, []);

  const save = useCallback((key: ProfileSetting, value: string | null, write: () => Promise<void>) => {
    if (!pending.current[key] && confirmed.current[key] === value) {
      if (mounted.current) setErrors(previous => previous[key] ? { ...previous, [key]: undefined } : previous);
      return Promise.resolve(true);
    }
    if (pending.current[key] && desired.current[key] === value) return requests.current[key]!;
    desired.current[key] = value;
    const revision = (revisions.current[key] ?? 0) + 1;
    revisions.current[key] = revision;
    pending.current[key] = (pending.current[key] ?? 0) + 1;
    window.clearTimeout(timers.current[key]);
    if (mounted.current) {
      setSaved(previous => ({ ...previous, [key]: undefined }));
      setErrors(previous => ({ ...previous, [key]: undefined }));
    }
    let didSave = false;
    const result = queue.current.then(async () => {
      if (revisions.current[key] !== revision || confirmed.current[key] === value) return;
      try {
        await write();
        confirmed.current[key] = value;
        didSave = true;
        if (mounted.current && revisions.current[key] === revision) {
          setSaved(previous => ({ ...previous, [key]: revision }));
          // Hold the check for five seconds, then allow the CSS fade to finish.
          timers.current[key] = window.setTimeout(() => {
            setSaved(previous => ({ ...previous, [key]: undefined }));
          }, 5300);
        }
      } catch (error) {
        if (mounted.current && revisions.current[key] === revision) {
          setErrors(previous => ({ ...previous, [key]: error instanceof Error ? error.message : "Couldn't save this setting." }));
        }
      }
    }).finally(() => { pending.current[key] = (pending.current[key] ?? 1) - 1; });
    queue.current = result;
    const request = result.then(() => didSave);
    requests.current[key] = request;
    return request;
  }, []);

  return { save, saved, errors };
}
