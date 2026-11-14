// Apply one folder selection across channel groups without touching other paths or filter rules.
import { useRef, useState } from 'react';
import { getChannels, getFolderSettings, getChannelCommercials, updateChannel, updateChannelCommercials } from '../../api/client';
import { channelPathGroup, defaultPathChoices, setChannelPath, type ChannelPathChoices, type ChannelPathGroup, type LibraryPathKind } from './channelPaths';
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
      const [folders, channels] = await Promise.all([getFolderSettings(), getChannels()]);
      if (!folders[kind].paths.some((entry) => entry.path === path)) throw new Error('This folder was removed. Close this dialog and reload Library Paths.');
      const paths = [...new Set([...folders.tv.paths, ...folders.movies.paths].map((p) => p.path))];
      const failed: string[] = [];
      for (const channel of channels) {
        const group = channelPathGroup(channel);
        if (!group) continue; // Demo channels have no library selection.
        const enabled = choices[group];
        try {
          if (kind === 'commercials') {
            const policy = await getChannelCommercials(channel.id);
            const selected = policy.effectiveFolders;
            const next = enabled ? [...new Set([...selected, path])] : selected.filter((p) => p !== path);
            await updateChannelCommercials(channel.id, {
              enabled: policy.enabled,
              folders: next,
              explicitFolders: true,
            });
          } else if (channel.filter) {
            await updateChannel(channel.id, {
              number: channel.number,
              name: channel.name,
              filter: setChannelPath(channel.filter, paths, path, enabled),
            });
          }
        } catch {
          failed.push(`${channel.number} (${channel.name})`);
        }
      }
      if (failed.length) throw new Error(`Some channels could not be updated: ${failed.join(', ')}. Save again to retry. Other channels have been updated.`);
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
      <p className="admin-section-hint">Save applies this path’s choices to all channel groups, overriding previous selections for this path. Other paths and channel settings are preserved.</p>
      <fieldset className="path-customize-options" disabled={busy}>
        {GROUPS.map(({ key, label }) => (
          <div className="admin-field-row" key={key}>
            <SettingToggle label={label} checked={choices[key]} onChange={(value) => setChoices((previous) => ({ ...previous, [key]: value }))} />
            <span>{label}</span>
          </div>
        ))}
      </fieldset>
      {error && <p role="alert" className="profile-field-error">{error}</p>}
      {busy && <p role="status" className="admin-section-hint">Applying path preferences…</p>}
      <div className="admin-table-actions">
        <button type="button" className="admin-btn" disabled={busy} onClick={() => void save()}>{busy ? 'Saving…' : 'Save'}</button>
        <button type="button" className="admin-btn admin-btn-secondary" disabled={busy} onClick={close}>Cancel</button>
      </div>
    </InfoModal>
  );
}
