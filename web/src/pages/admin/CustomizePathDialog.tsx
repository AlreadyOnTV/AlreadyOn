// Apply one folder selection across channel groups without touching other paths or filter rules.
import { useEffect, useRef, useState } from 'react';
import { customizeLibraryPath, getLibraryPathCounts } from '../../api/client';
import { announceLibraryUpdate } from './libraryUpdateEvents';
import { defaultPathChoices, type ChannelPathChoices, type ChannelPathGroup, type LibraryPathKind } from './channelPaths';
import InfoModal from './InfoModal';
import SettingToggle from './SettingToggle';

const GROUPS: { key: ChannelPathGroup; label: string }[] = [
  { key: 'movies', label: 'Enable for Movie Channels' },
  { key: 'tv', label: 'Enable for TV Show Channels' },
  { key: 'custom', label: 'Enable for Custom Channels' },
];

export default function CustomizePathDialog({ path, kind, onClose }: {
  path: string;
  kind: LibraryPathKind;
  onClose: () => void;
}) {
  const [choices, setChoices] = useState<ChannelPathChoices>(() => defaultPathChoices(kind));
  const [counts, setCounts] = useState<Record<ChannelPathGroup, number> | null>(null);
  const [countError, setCountError] = useState<string | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    getLibraryPathCounts(kind, path, controller.signal).then((next) => {
      if (!controller.signal.aborted) { setCounts(next); setCountError(null); }
    }).catch((err: Error) => { if (!controller.signal.aborted) setCountError(err.message); });
    return () => controller.abort();
  }, [kind, path]);
  const [busy, setBusy] = useState(false);
  const saving = useRef(false);
  const [error, setError] = useState<string | null>(null);
  const close = () => { if (!saving.current) onClose(); };

  const save = async () => {
    if (saving.current) return;
    saving.current = true;
    setBusy(true);
    setError(null);
    try {
      const state = await customizeLibraryPath({ kind, path, ...choices });
      announceLibraryUpdate(state);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not save path preferences');
    } finally {
      saving.current = false;
      setBusy(false);
    }
  };

  return (
    <InfoModal open title="Customize Library Path" onClose={close}>
      <p className="folder-path">{path}</p>
      <p className="admin-section-hint">Save stores this path’s choices for all channel groups, overriding previous selections for this path. Channels update in the background. Other paths and channel settings are preserved. Counts show saved selections.</p>
      <fieldset className="path-customize-options" disabled={busy}>
        {GROUPS.map(({ key, label }) => (
          <div className="admin-field-row path-customize-row" key={key}>
            <SettingToggle label={label} checked={choices[key]} onChange={(value) => setChoices((previous) => ({ ...previous, [key]: value }))} />
            <span>{label}</span>
            <span className="folder-status path-customize-count">(Channels {counts?.[key] ?? (countError ? "—" : "…")})</span>
          </div>
        ))}
      </fieldset>
      {countError && <p role="alert" className="profile-field-error">{countError}</p>}
      {error && <p role="alert" className="profile-field-error">{error}</p>}
      {busy && <p role="status" className="admin-section-hint">Applying path preferences…</p>}
      <div className="admin-table-actions">
        <button type="button" className="admin-btn" disabled={busy} onClick={() => void save()}>{busy ? 'Saving…' : 'Save'}</button>
        <button type="button" className="admin-btn admin-btn-secondary" disabled={busy} onClick={close}>Cancel</button>
      </div>
    </InfoModal>
  );
}
