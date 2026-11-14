import { announceLibraryUpdate } from '../../api/libraryUpdateEvents';
// Enabled/Mode/etc. are backed by GET/PUT /api/settings/commercials (see
// server/internal/commercial and #24). The commercials folders are stored
// on the server too (GET/PUT /api/settings/folders), shown as the built-in
// default until an admin customizes them.
import { useEffect, useRef, useState } from "react";
import {
  getCommercialSettings,
  getFolderSettings,
  updateCommercialSettings,
  updateFolderSettings,
  type CommercialMode,
  type CommercialSettings,
  type FolderPathStatus,
  type FolderSettings,
} from "../../api/client";
import FolderListCard from "./FolderListCard";
import { draftFromSaved, draftValues, type ListDraft } from "./folderDraft";
import { commercialSaveQueue, type CommercialSaveState } from "./commercialSaveQueue";
import "./AdminShared.css";

// The API models this as mode + intervalMinutes (two interval settings
// sharing one "interval" mode), but the dropdown itself should offer
// each interval as its own option rather than a mode picker plus a
// second, conditionally-shown interval picker.
type ModeSelection = "between-only" | "interval-15" | "interval-30" | "interval-60";

const MODE_SELECTION_OPTIONS: { value: ModeSelection; label: string }[] = [
  { value: "between-only", label: "Between Episodes/Movies Only (Default)" },
  { value: "interval-15", label: "Every 15 Minutes" },
  { value: "interval-30", label: "Every 30 Minutes" },
  { value: "interval-60", label: "Every 60 Minutes" },
];

function toSelection(mode: CommercialMode, intervalMinutes: number): ModeSelection {
  if (mode === "between-only") return "between-only";
  return `interval-${intervalMinutes}` as ModeSelection;
}

function fromSelection(selection: ModeSelection): { mode: CommercialMode; intervalMinutes: number } {
  if (selection === "between-only") {
    return { mode: "between-only", intervalMinutes: 30 };
  }
  return { mode: "interval", intervalMinutes: Number(selection.slice("interval-".length)) };
}

const COMMERCIALS_PER_BREAK_OPTIONS = [1, 2, 3, 4, 5];

export default function CommercialsPage() {
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [volumeOffset, setVolumeOffset] = useState("0");

  const [enabled, setEnabled] = useState(false);
  const [selection, setSelection] = useState<ModeSelection>("between-only");
  const [tvCount, setTvCount] = useState(2);
  const [movieCount, setMovieCount] = useState(5);

  const latestSettings = useRef<CommercialSettings | null>(null);
  const mounted = useRef(true);
  const savedTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const [saveState, setSaveState] = useState<CommercialSaveState | 'idle'>('idle');
  const [saveError, setSaveError] = useState<string | null>(null);
  const [volumeError, setVolumeError] = useState<string | null>(null);
  const queue = useRef<ReturnType<typeof commercialSaveQueue> | null>(null);
  useEffect(() => {
    mounted.current = true;
    queue.current = commercialSaveQueue(updateCommercialSettings, (state, error) => {
      if (!mounted.current) return;
      clearTimeout(savedTimer.current);
      setSaveState(state);
      setSaveError(error ?? null);
      if (state === 'saved') savedTimer.current = setTimeout(() => { if (mounted.current) setSaveState('idle'); }, 2000);
    });
    return () => {
      mounted.current = false;
      clearTimeout(savedTimer.current);
      // Navigating away must not discard the last debounced edit.
      void queue.current?.flush();
    };
  }, []);

  const [savedFolders, setSavedFolders] = useState<FolderSettings | null>(null);
  const [folderLoadError, setFolderLoadError] = useState<string | null>(null);
  const folderPending = useRef(false);
  const [folderSaving, setFolderSaving] = useState(false);
  const [pathDraft, setPathDraft] = useState<ListDraft>(null);

  useEffect(() => {
    getFolderSettings()
      .then((s) => {
        setSavedFolders(s);
        setPathDraft(draftFromSaved(s.commercials.source, s.commercials.paths.map((p) => p.path)));
        setFolderLoadError(null);
      })
      .catch((err: Error) => setFolderLoadError(err.message));

    getCommercialSettings()
      .then((s) => {
        const loadedSelection = toSelection(s.mode, s.intervalMinutes);
        latestSettings.current = { ...s, volumeOffsetDb: s.volumeOffsetDb ?? 0 };
        setVolumeOffset(String(s.volumeOffsetDb ?? 0));
        setEnabled(s.enabled);
        setSelection(loadedSelection);
        setTvCount(s.tvCommercialsPerBreak);
        setMovieCount(s.movieCommercialsPerBreak);
        setLoadError(null);
      })
      .catch((err: Error) => setLoadError(err.message))
      .finally(() => setLoading(false));
  }, []);

  // Folder edits and break preferences persist independently without reloading the form.
  const saveFolders = async (next: ListDraft) => {
    if (folderPending.current) return;
    folderPending.current = true;
    setFolderSaving(true);
    setFolderLoadError(null);
    try {
      const saved = await updateFolderSettings({ commercials: next });
      announceLibraryUpdate();
      setSavedFolders(saved);
      setPathDraft(draftFromSaved(saved.commercials.source, saved.commercials.paths.map(p => p.path)));
    } catch (err) {
      setFolderLoadError(err instanceof Error ? err.message : 'Could not save commercial folders');
    } finally {
      folderPending.current = false;
      setFolderSaving(false);
    }
  };

  const pathError =
    enabled && savedFolders !== null && draftValues(pathDraft, savedFolders.commercials.default).length === 0
      ? "Add at least one commercials folder while commercial breaks are enabled."
      : null;

  const changeSettings = (patch: Partial<CommercialSettings>) => {
    const previous = latestSettings.current;
    if (!previous || Object.entries(patch).every(([key, value]) => previous[key as keyof CommercialSettings] === value)) return;
    const next = { ...previous, ...patch };
    latestSettings.current = next;
    queue.current?.enqueue(next);
  };
  const changeVolume = (value: string) => {
    setVolumeOffset(value);
    if (!value.trim() || !Number.isFinite(Number(value)) || Number(value) < -30 || Number(value) > 12) {
      setVolumeError('Volume offset must be between -30 and 12 dB.');
      return;
    }
    setVolumeError(null);
    changeSettings({ volumeOffsetDb: Number(value) });
  };

  if (loading) {
    return (
      <div className="admin-section">
        <p className="admin-section-hint">Loading…</p>
      </div>
    );
  }

  return (
    <div className="admin-section">
      {loadError && <p className="profile-field-error">{loadError}</p>}

      {savedFolders && (
        <FolderListCard
          title="Commercial Folders"
          placeholder="/path/to/commercials"
          addLabel="+ Add folder"
          emptyText="No commercial folders configured."
          draft={pathDraft}
          defaults={savedFolders.commercials.default}
          counts={Object.fromEntries(savedFolders.commercials.paths.map(p => [p.path, p.count ?? 0]))}
          statuses={Object.fromEntries(savedFolders.commercials.paths.map((p): [string, FolderPathStatus] => [p.path, p.status]))}
          requireAbsolute
          customizeKind="commercials"
          disabled={folderSaving}
          onChange={next => void saveFolders(next)}
        />
      )}

      <div className="admin-card commercial-settings">
        <h3>Commercial Breaks</h3>
        <div className="admin-field-row">
          <label>Enabled</label>
          <button
            type="button"
            className={"admin-badge" + (enabled ? " admin-badge-on" : "")}
            onClick={() => { setEnabled(!enabled); changeSettings({ enabled: !enabled }); }}
          >
            {enabled ? "On" : "Off"}
          </button>
        </div>
        <div className="admin-field-row">
          <label>Mode</label>
          <select
            value={selection}
            onChange={(e) => { const next = e.target.value as ModeSelection; setSelection(next); changeSettings(fromSelection(next)); }}
            disabled={!enabled}
          >
            {MODE_SELECTION_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
        </div>
        <div className="admin-field-row">
          <label>TV Commercials Per Break</label>
          <select value={tvCount} onChange={(e) => { const next = Number(e.target.value); setTvCount(next); changeSettings({ tvCommercialsPerBreak: next }); }} disabled={!enabled}>
            {COMMERCIALS_PER_BREAK_OPTIONS.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </div>
        <div className="admin-field-row">
          <label>Movie Commercials Per Break</label>
          <select value={movieCount} onChange={(e) => { const next = Number(e.target.value); setMovieCount(next); changeSettings({ movieCommercialsPerBreak: next }); }} disabled={!enabled}>
            {COMMERCIALS_PER_BREAK_OPTIONS.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </div>
        <div className="admin-field-row">
          <label htmlFor="commercial-volume-offset">Commercial Volume Offset (dB)</label>
          <input id="commercial-volume-offset" type="number" min="-30" max="12" step="0.5" value={volumeOffset} aria-invalid={!!volumeError} onChange={e => changeVolume(e.target.value)} />
        </div>
        {volumeError && <p className="profile-field-error" role="alert">{volumeError}</p>}
        {saveError && <p className="profile-field-error" role="alert">{saveError} <button type="button" className="admin-btn admin-btn-secondary" onClick={() => void queue.current?.retry()}>Retry</button></p>}
        {(saveState === 'saving' || saveState === 'saved') && <p className="admin-section-hint" role="status">{saveState === 'saving' ? 'Saving commercial settings…' : 'Commercial settings saved.'}</p>}
      </div>

      {folderLoadError && <p className="profile-field-error">{folderLoadError}</p>}
      {pathError && <p className="profile-field-error">{pathError}</p>}

    </div>
  );
}
