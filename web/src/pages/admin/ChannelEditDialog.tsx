// Create or edit any channel, built-in or custom, in a centered dialog: its
// number and name, and the filter its lineup is built from. Saving a filter
// rebuilds the channel's lineup right away (see the server's channels API).
// Its commercial settings are saved alongside through the channel commercials API.
import { useEffect, useState } from 'react';
import { createChannel, updateChannel, DEFAULT_CHANNEL_FILTER, type Channel, type ChannelFilter } from '../../api/client';
import ChannelCommercialsFields, { loadChannelCommercials, saveChannelCommercials, type ChannelCommercialsDraft } from './ChannelCommercialsFields';
import ChannelFilterForm from './ChannelFilterForm';
import ChannelFilterPreview from './ChannelFilterPreview';
import InfoModal from './InfoModal';

// The fallback demo channels (server/internal/lineup/demo.go) come and go on
// their own, so they have no filter to edit.
const isDemoChannel = (n: number) => n >= 196 && n <= 199;

export default function ChannelEditDialog({
  channel,
  suggestedNumber,
  onClose,
  onSaved,
}: {
  // Absent when creating a channel.
  channel?: Channel;
  suggestedNumber: number;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [number, setNumber] = useState(String(channel?.number ?? suggestedNumber));
  const [name, setName] = useState(channel?.name ?? '');
  const [filter, setFilter] = useState<ChannelFilter>(channel?.filter ?? DEFAULT_CHANNEL_FILTER);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const [commercials, setCommercials] = useState<ChannelCommercialsDraft | null>(null);

  const hasFilter = !channel || !isDemoChannel(channel.number);

  useEffect(() => {
    if (!hasFilter) return;
    let active = true;
    loadChannelCommercials(channel?.id)
      .then((d) => active && setCommercials(d))
      .catch((err: Error) => active && setError(err.message));
    return () => {
      active = false;
    };
  }, [channel?.id, hasFilter]);

  const handleSave = async () => {
    const parsedNumber = Number(number.trim());
    if (!/^\d+$/.test(number.trim()) || parsedNumber < 1) {
      setError('The channel number must be a whole number, 1 or higher.');
      return;
    }
    if (name.trim() === '') {
      setError('The channel needs a name.');
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const draft = { number: parsedNumber, name: name.trim(), ...(hasFilter ? { filter } : {}) };
      const saved = channel ? await updateChannel(channel.id, draft) : await createChannel(draft);
      if (commercials) await saveChannelCommercials(saved.id, commercials);
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save channel');
      setSaving(false);
    }
  };

  return (
    <InfoModal open className="channel-editor" title={channel ? `Edit channel ${channel.number}` : 'Create Custom Channel'} onClose={onClose}>
      <div className="admin-field-row">
        <label>Channel Number</label>
        <input type="text" inputMode="numeric" value={number} onChange={(e) => setNumber(e.target.value)} />
      </div>
      <div className="admin-field-row">
        <label>Channel Name</label>
        <input type="text" value={name} placeholder="Name" autoFocus onChange={(e) => setName(e.target.value)} />
      </div>
      {hasFilter && <ChannelFilterForm filter={filter} onChange={setFilter} />}
      {hasFilter && commercials && <ChannelCommercialsFields draft={commercials} onChange={setCommercials} />}

      {error && <p className="profile-field-error">{error}</p>}
      {hasFilter && <ChannelFilterPreview filter={filter} />}
      <div className="admin-table-actions">
        <button className="admin-btn" onClick={() => void handleSave()} disabled={saving}>
          {saving ? 'Saving…' : 'Save'}
        </button>
        <button className="admin-btn admin-btn-secondary" onClick={onClose} disabled={saving}>
          Cancel
        </button>
      </div>
    </InfoModal>
  );
}
