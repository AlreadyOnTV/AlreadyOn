import { getFilterOptions, type FilterOption } from '../../api/client';
import ConfigureFilters from './ConfigureFilters';
// Real backend (GET/POST/PUT/DELETE /api/channels — see server/internal/channel
// and server/internal/httpapi/channels.go), unlike most other Admin pages,
// which still simulate their data locally.
import { useEffect, useState } from 'react';
import {
  getChannels,
  deleteChannel,
  setChannelVisibility,
  bulkSetChannelsVisibility,
  refreshSchedule,
  type Channel,
  type ChannelFilter,
  type ChannelVisibility,
} from '../../api/client';
import ChannelEditDialog from './ChannelEditDialog';
import { EyeIcon, PencilIcon, TrashIcon } from './AdminIcons';
import { useRefreshTimes } from './useRefreshTimes';
import { LastTime } from './LastTime';

import './AdminShared.css';

// Channels 100-199 are reserved for AlreadyOn's built-in defaults (see
// server/internal/channel: DefaultChannelRangeStart/End) — custom
// channels must land outside that range.
const CUSTOM_RANGE_START = 200;

const RATING_LABELS = { young_child: 'Kids', teen: 'Teens', adult: 'Unrestricted' } as const;
// The per-row button steps through these in order; Hide looks like Edit and
// Delete, while Show and Auto look like Refresh Schedule.
const VISIBILITY_ORDER: ChannelVisibility[] = ['hide', 'show', 'auto'];
const VISIBILITY_LABELS: Record<ChannelVisibility, string> = { hide: 'Hide', show: 'Show', auto: 'Auto' };
const VISIBILITY_HINTS: Record<ChannelVisibility, string> = {
  hide: 'Never shown in the guide. Click for Show.',
  show: 'Always shown in the guide. Click for Auto.',
  auto: 'Shown in the guide while it has shows or movies. Click for Hide.',
};

const MEDIA_LABELS = { tv: 'TV Shows', movie: 'Movies', both: 'TV Shows And Movies' } as const;
const ERA_LABELS = { modern: 'modern', nostalgia: 'Classics', both: '' } as const;

function describeFilter(f: ChannelFilter): string {
  const parts: string[] = [MEDIA_LABELS[f.mediaType]];
  if (ERA_LABELS[f.era]) parts.push(ERA_LABELS[f.era]);
  if (f.genres.length > 0) parts.push(f.genres.join(', '));
  if (f.titleContains) parts.push(`title contains "${f.titleContains}"`);
  return parts.join(' · ');
}

export default function ChannelsPage() {
  const [ages, setAges] = useState<FilterOption[]>([]);
  const [sort, setSort] = useState<{ key: 'number' | 'name' | 'age' | 'items'; reverse: boolean }>({ key: 'number', reverse: false });
  const ageName = (c: Channel) =>
    ages.find((a) => a.id === (c.filter?.ageRestrictionId ?? c.filter?.audienceTier))?.name ?? (c.filter ? RATING_LABELS[c.filter.audienceTier] : '');
  const sortBy = (key: typeof sort.key) => setSort((s) => ({ key, reverse: s.key === key ? !s.reverse : false }));
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Channel | 'new' | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);
  const [refreshBusy, setRefreshBusy] = useState(false);
  const [refreshMessage, setRefreshMessage] = useState<string | null>(null);

  const { times: refreshTimes, reload: reloadRefreshTimes } = useRefreshTimes();

  const fetchChannels = () => {
    void getFilterOptions()
      .then((c) => setAges(c.age))
      .catch((e: Error) => setLoadError(e.message));
    return getChannels()
      .then((data) => {
        setChannels(data);
        setLoadError(null);
      })
      .catch((err: Error) => setLoadError(err.message))
      .finally(() => setLoading(false));
  };

  // `loading` already starts true (see useState above), so the mount
  // effect itself never needs to set it — only fetchChannels()'s own
  // async completion (in .finally, not synchronously here) does.
  useEffect(() => {
    fetchChannels();
  }, []);

  // The table stays on screen while it reloads, so the page keeps its scroll position.
  const refresh = () => {
    void fetchChannels();
  };

  const nextCustomNumber = () => {
    const customNumbers = channels.map((c) => c.number).filter((n) => n >= CUSTOM_RANGE_START);
    return customNumbers.length > 0 ? Math.max(...customNumbers) + 1 : CUSTOM_RANGE_START;
  };

  const handleSaved = () => {
    setEditing(null);
    refresh();
  };

  const handleDeleteClick = async (id: number) => {
    if (confirmDeleteId !== id) {
      setConfirmDeleteId(id);
      return;
    }
    try {
      await deleteChannel(id);
      setConfirmDeleteId(null);
      refresh();
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : 'Failed to delete channel');
    }
  };

  const toggleSelected = (id: number) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const visibleChannels = [...channels]
    .sort((a, b) => {
      let comparison =
        sort.key === 'number'
          ? a.number - b.number
          : sort.key === 'items'
            ? a.itemCount - b.itemCount
            : sort.key === 'name'
              ? a.name.localeCompare(b.name)
              : ageName(a).localeCompare(ageName(b));
      return (sort.reverse ? -comparison : comparison) || a.number - b.number;
    });

  const header = (key: typeof sort.key, label: string) => (
    <th aria-sort={sort.key === key ? (sort.reverse ? 'descending' : 'ascending') : 'none'}>
      <button className="table-sort" onClick={() => sortBy(key)}>
        {label}
        {sort.key === key ? (sort.reverse ? ' ▾' : ' ▴') : ''}
      </button>
    </th>
  );

  const handleCycleVisibility = async (c: Channel) => {
    const next = VISIBILITY_ORDER[(VISIBILITY_ORDER.indexOf(c.visibility) + 1) % VISIBILITY_ORDER.length];
    try {
      await setChannelVisibility(c.id, next);
      refresh();
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : 'Failed to update channel visibility');
    }
  };

  const handleBulkSetVisibility = async (visibility: ChannelVisibility) => {
    if (selectedIds.size === 0) return;
    setBulkBusy(true);
    try {
      await bulkSetChannelsVisibility(Array.from(selectedIds), visibility);
      setSelectedIds(new Set());
      refresh();
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : 'Failed to update channel visibility');
    } finally {
      setBulkBusy(false);
    }
  };

  const handleRefreshSchedule = async () => {
    setRefreshBusy(true);
    setRefreshMessage(null);
    try {
      const result = await refreshSchedule();
      const parts = [`Rebuilt ${result.channelsRefreshed} channel lineup(s), regenerated ${result.schedulesRegenerated} schedule(s).`];
      if (!result.classified) parts.push('TMDb classification skipped (no API key configured).');
      setRefreshMessage(parts.join(' '));
      reloadRefreshTimes();
      refresh();
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : 'Failed to refresh schedule');
    } finally {
      setRefreshBusy(false);
    }
  };

  return (
    <div className="admin-section">
      {loadError && <p className="profile-field-error">{loadError}</p>}

      <div className="admin-card">
        <h3>Guide Schedule</h3>
        <p className="admin-section-hint">
          Force a manual regeneration of every channel's schedule, and rebuild every channel's lineup from its filter. This also happens automatically when new
          media is detected, and schedules are topped up every hour. Editing a channel rebuilds it right away.
        </p>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <button className="admin-btn" onClick={handleRefreshSchedule} disabled={refreshBusy}>
            {refreshBusy ? 'Refreshing…' : 'Refresh Schedule'}
          </button>
          <LastTime label="Last Updated" at={refreshTimes?.scheduleRefreshedAt} />
        </div>
        {refreshMessage && <p className="admin-section-hint">{refreshMessage}</p>}
      </div>

      <ConfigureFilters onChanged={refresh} />
      <div className="admin-card">
        <h3>Configure Channels</h3>
        {loading ? (
          <p className="admin-section-hint">Loading…</p>
        ) : (
          <>
            <div className="admin-table-actions" style={{ marginBottom: 12, justifyContent: 'flex-end' }}>
              <button
                className="admin-btn admin-btn-secondary"
                onClick={() => void handleBulkSetVisibility('hide')}
                disabled={bulkBusy || selectedIds.size === 0}
              >
                Hide Selected
              </button>
              <button
                className="admin-btn admin-btn-secondary"
                onClick={() => void handleBulkSetVisibility('show')}
                disabled={bulkBusy || selectedIds.size === 0}
              >
                Show Selected
              </button>
              <button
                className="admin-btn admin-btn-secondary"
                onClick={() => void handleBulkSetVisibility('auto')}
                disabled={bulkBusy || selectedIds.size === 0}
              >
                Set to Auto
              </button>
              <button className="admin-btn" onClick={() => setEditing('new')}>
                + Create Custom Channel
              </button>
            </div>
            <table className="admin-table configured-channels-table">
              <thead>
                <tr>
                  <th className="channel-bulk-heading">
                    <button className="text-action" onClick={() => setSelectedIds(new Set(visibleChannels.map((c) => c.id)))}>
                      Select All
                    </button>{' '}
                    /{' '}
                    <button className="text-action" onClick={() => setSelectedIds(new Set())}>
                      Deselect All
                    </button>
                  </th>
                  {header('number', '#')}
                  {header('name', 'Name')}
                  {header('age', 'Restrictions')}
                  {header('items', 'Matches')}
                </tr>
              </thead>
              <tbody>
                {visibleChannels.map((c) => (
                  <tr key={c.id}>
                    <td className="channel-controls-cell">
                      <div className="channel-primary-actions">
                        <label className="channel-selection-toggle" title={selectedIds.has(c.id) ? 'Deselect channel' : 'Select channel'}>
                          <input type="checkbox" checked={selectedIds.has(c.id)} onChange={() => toggleSelected(c.id)} aria-label={`Select ${c.name}`} />
                          <span />
                        </label>
                        <button
                          className="admin-icon-btn channel-visibility-action"
                          aria-label={`Visibility: ${VISIBILITY_LABELS[c.visibility]}`}
                          title={`${VISIBILITY_LABELS[c.visibility]} — ${VISIBILITY_HINTS[c.visibility]}`}
                          onClick={() => void handleCycleVisibility(c)}
                        >
                          {c.visibility === 'auto' ? <span className="channel-auto-icon">A</span> : <EyeIcon closed={c.visibility === 'hide'} />}
                        </button>
                      </div>
                      <div className="channel-edit-actions">
                        <button className="admin-icon-btn" aria-label={`Edit ${c.name}`} title="Edit channel" onClick={() => setEditing(c)}>
                          <PencilIcon />
                        </button>
                        <button
                          className={'admin-icon-btn' + (confirmDeleteId === c.id ? ' admin-btn-danger' : '')}
                          aria-label={confirmDeleteId === c.id ? `Confirm delete ${c.name}` : `Delete ${c.name}`}
                          title={confirmDeleteId === c.id ? 'Confirm delete channel' : 'Delete channel'}
                          onClick={() => handleDeleteClick(c.id)}
                        >
                          <TrashIcon />
                        </button>
                      </div>
                    </td>
                    <td>{c.number}</td>
                    <td>
                      {c.name}
                      {c.filter && <div className="admin-section-hint channel-filter-summary">{describeFilter(c.filter)}</div>}
                    </td>
                    <td>{ageName(c)}</td>
                    <td>{c.itemCount}</td>
                  </tr>
                ))}
                {visibleChannels.length === 0 && (
                  <tr>
                    <td colSpan={5} className="admin-section-hint">
                      No channels yet.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </>
        )}
      </div>

      {editing && (
        <ChannelEditDialog
          channel={editing === 'new' ? undefined : editing}
          suggestedNumber={nextCustomNumber()}
          onClose={() => setEditing(null)}
          onSaved={handleSaved}
        />
      )}
    </div>
  );
}
