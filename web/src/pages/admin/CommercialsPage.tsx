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
  type FolderPathStatus,
  type FolderSettings,
} from "../../api/client";
import FolderListCard from "./FolderListCard";
import { draftFromSaved, draftValues, type ListDraft } from "./folderDraft";
import SettingsActions from "./SettingsActions";
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

  const [savedVolumeOffset, setSavedVolumeOffset] = useState(0);
  const [volumeOffset, setVolumeOffset] = useState("0");
  const [savedEnabled, setSavedEnabled] = useState(false);
  const [savedSelection, setSavedSelection] = useState<ModeSelection>("between-only");
  const [savedTvCount, setSavedTvCount] = useState(2);
  const [savedMovieCount, setSavedMovieCount] = useState(5);

  const [enabled, setEnabled] = useState(false);
  const [selection, setSelection] = useState<ModeSelection>("between-only");
  const [tvCount, setTvCount] = useState(2);
  const [movieCount, setMovieCount] = useState(5);

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
        setSavedVolumeOffset(s.volumeOffsetDb ?? 0);
        setVolumeOffset(String(s.volumeOffsetDb ?? 0));
        setSavedEnabled(s.enabled);
        setSavedSelection(loadedSelection);
        setSavedTvCount(s.tvCommercialsPerBreak);
        setSavedMovieCount(s.movieCommercialsPerBreak);
        setEnabled(s.enabled);
        setSelection(loadedSelection);
        setTvCount(s.tvCommercialsPerBreak);
        setMovieCount(s.movieCommercialsPerBreak);
        setLoadError(null);
      })
      .catch((err: Error) => setLoadError(err.message))
      .finally(() => setLoading(false));
  }, []);

  // Persist folders independently so Cancel only discards commercial break settings.
  const saveFolders = async (next: ListDraft) => {
    if (folderPending.current) return;
    folderPending.current = true;
    setFolderSaving(true);
    setFolderLoadError(null);
    try {
      const saved = await updateFolderSettings({ commercials: next });
      setSavedFolders(saved);
      setPathDraft(draftFromSaved(saved.commercials.source, saved.commercials.paths.map(p => p.path)));
    } catch (err) {
      setFolderLoadError(err instanceof Error ? err.message : 'Could not save commercial folders');
    } finally {
      folderPending.current = false;
      setFolderSaving(false);
    }
  };

  const hasChanges =
    Number(volumeOffset) !== savedVolumeOffset ||
    enabled !== savedEnabled ||
    selection !== savedSelection ||
    tvCount !== savedTvCount ||
    movieCount !== savedMovieCount;

  const pathError =
    enabled && savedFolders !== null && draftValues(pathDraft, savedFolders.commercials.default).length === 0
      ? "Add at least one commercials folder while commercial breaks are enabled."
      : null;

  const handleSave = async () => {
    // The server refreshes the schedule when break timing/counts change.
    // Volume-only changes apply to new streams without rebuilding it.
    const { mode, intervalMinutes } = fromSelection(selection);
    if (volumeOffset.trim() === "" || !Number.isFinite(Number(volumeOffset)) || Number(volumeOffset) < -30 || Number(volumeOffset) > 12) throw new Error("Volume offset must be between -30 and 12 dB.");
    await updateCommercialSettings({
      volumeOffsetDb: Number(volumeOffset),
      enabled,
      mode,
      intervalMinutes,
      tvCommercialsPerBreak: tvCount,
      movieCommercialsPerBreak: movieCount,
    });
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
            onClick={() => setEnabled((e) => !e)}
          >
            {enabled ? "On" : "Off"}
          </button>
        </div>
        <div className="admin-field-row">
          <label>Mode</label>
          <select
            value={selection}
            onChange={(e) => setSelection(e.target.value as ModeSelection)}
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
          <select value={tvCount} onChange={(e) => setTvCount(Number(e.target.value))} disabled={!enabled}>
            {COMMERCIALS_PER_BREAK_OPTIONS.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </div>
        <div className="admin-field-row">
          <label>Movie Commercials Per Break</label>
          <select value={movieCount} onChange={(e) => setMovieCount(Number(e.target.value))} disabled={!enabled}>
            {COMMERCIALS_PER_BREAK_OPTIONS.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </div>
        <div className="admin-field-row">
          <label htmlFor="commercial-volume-offset">Commercial Volume Offset (dB)</label>
          <input id="commercial-volume-offset" type="number" min="-30" max="12" step="0.5" value={volumeOffset} onChange={e => setVolumeOffset(e.target.value)} />
        </div>
        <SettingsActions hasChanges={hasChanges && !folderSaving} onSave={handleSave} />
      </div>

      {folderLoadError && <p className="profile-field-error">{folderLoadError}</p>}
      {pathError && <p className="profile-field-error">{pathError}</p>}

    </div>
  );
}
