// TV folders, movie folders and exclusion patterns are stored on the
// server (GET/PUT /api/settings/folders); a list an admin hasn't customized
// shows the server's built-in default. The polling interval is a separate
// setting, backed by GET/PUT /api/settings/media.
//
// Every change is saved as soon as it is made, and the server rescans on
// its own. Changes made in quick succession are sent one at a time, each
// after the previous request has finished.
import { useCallback, useEffect, useRef, useState } from "react";
import {
  getFolderSettings,
  getMediaSettings,
  refreshMedia,
  updateFolderSettings,
  updateMediaSettings,
  type FolderPathStatus,
  type FolderSettings,
} from "../../api/client";
import { useUsers } from "../../users/UsersContext";
import CommercialsPage from "./CommercialsPage";
import FolderListCard from "./FolderListCard";
import { draftFromSaved, type ListDraft } from "./folderDraft";
import { LastTime, useRefreshTimes } from "./useRefreshTimes";
import "./AdminShared.css";

const POLL_INTERVAL_OPTIONS = [
  { value: 15, label: "15 Minutes" },
  { value: 30, label: "30 Minutes" },
  { value: 60, label: "1 Hour (Default)" },
  { value: 120, label: "2 Hours" },
  { value: 240, label: "4 Hours" },
  { value: 480, label: "8 Hours" },
  { value: 720, label: "12 Hours" },
  { value: 1440, label: "24 Hours" },
];

function statusMap(
  paths: { path: string; status: FolderPathStatus }[],
): Record<string, FolderPathStatus> {
  return Object.fromEntries(paths.map((p) => [p.path, p.status]));
}

export default function MediaPage() {
  const { currentUserId } = useUsers();
  const [saved, setSaved] = useState<FolderSettings | null>(null);
  const [folderLoadError, setFolderLoadError] = useState<string | null>(null);
  const [tvDraft, setTvDraft] = useState<ListDraft>(null);
  const [movieDraft, setMovieDraft] = useState<ListDraft>(null);

  const [pollLoading, setPollLoading] = useState(true);
  const [pollLoadError, setPollLoadError] = useState<string | null>(null);
  const [pollInterval, setPollInterval] = useState(60);

  const [refreshBusy, setRefreshBusy] = useState(false);
  const [refreshMessage, setRefreshMessage] = useState<string | null>(null);
  const [refreshError, setRefreshError] = useState<string | null>(null);
  const [saveState, setSaveState] = useState<"idle" | "saving" | "saved">(
    "idle",
  );
  const [saveError, setSaveError] = useState<string | null>(null);
  // The tail of the save queue and how many saves are waiting or running.
  const queue = useRef<Promise<void>>(Promise.resolve());
  const pending = useRef(0);

  const applyFolders = useCallback((s: FolderSettings) => {
    setSaved(s);
    setTvDraft(
      draftFromSaved(
        s.tv.source,
        s.tv.paths.map((p) => p.path),
      ),
    );
    setMovieDraft(
      draftFromSaved(
        s.movies.source,
        s.movies.paths.map((p) => p.path),
      ),
    );
  }, []);

  useEffect(() => {
    getFolderSettings()
      .then((s) => {
        applyFolders(s);
        setFolderLoadError(null);
      })
      .catch((err: Error) => setFolderLoadError(err.message));

    getMediaSettings()
      .then((s) => {
        setPollInterval(s.pollIntervalMinutes);
        setPollLoadError(null);
      })
      .catch((err: Error) => setPollLoadError(err.message))
      .finally(() => setPollLoading(false));
  }, [applyFolders]);

  // Runs a save after every earlier one has finished. Once the queue is empty
  // the page reloads from the server, which refreshes each folder's status
  // and, after a failed save, puts back what was actually stored.
  const enqueue = (save: () => Promise<unknown>) => {
    pending.current += 1;
    setSaveState("saving");
    setSaveError(null);
    queue.current = queue.current.then(async () => {
      try {
        await save();
      } catch (err) {
        setSaveError(
          err instanceof Error ? err.message : "Failed to save the change",
        );
      } finally {
        pending.current -= 1;
        if (pending.current === 0) {
          try {
            const [folders, media] = await Promise.all([
              getFolderSettings(),
              getMediaSettings(),
            ]);
            applyFolders(folders);
            setPollInterval(media.pollIntervalMinutes);
          } catch {
            // The next change or a page reload will resync.
          }
          setSaveState((s) => (pending.current === 0 ? "saved" : s));
        }
      }
    });
  };

  const changeTv = (next: ListDraft) => {
    setTvDraft(next);
    enqueue(() => updateFolderSettings({ tv: next }));
  };
  const changeMovies = (next: ListDraft) => {
    setMovieDraft(next);
    enqueue(() => updateFolderSettings({ movies: next }));
  };
  const changePollInterval = (minutes: number) => {
    setPollInterval(minutes);
    enqueue(() => updateMediaSettings({ pollIntervalMinutes: minutes }));
  };

  const { times: refreshTimes, reload: reloadRefreshTimes } = useRefreshTimes();

  const handleRefreshMedia = async () => {
    setRefreshBusy(true);
    setRefreshMessage(null);
    setRefreshError(null);
    try {
      const { filesAdded, filesMissing } = await refreshMedia(currentUserId);
      setSaved(await getFolderSettings());
      const plural = (n: number) => `${n} file${n === 1 ? "" : "s"}`;
      setRefreshMessage(
        `Scan finished: ${plural(filesAdded)} added, ${plural(filesMissing)} no longer found.`,
      );
    } catch (err) {
      setRefreshError(
        err instanceof Error ? err.message : "Failed to refresh media",
      );
    } finally {
      setRefreshBusy(false);
      reloadRefreshTimes();
    }
  };

  if (saved === null && !folderLoadError) {
    return (
      <div className="admin-section">
        <p className="admin-section-hint">Loading…</p>
      </div>
    );
  }

  return (
    <div className="admin-section">
      {folderLoadError && (
        <p className="profile-field-error">{folderLoadError}</p>
      )}
      {saveError && <p className="profile-field-error">{saveError}</p>}
      {saveState !== "idle" && !saveError && (
        <p className="admin-section-hint media-save-status" role="status">
          {saveState === "saving" ? "Saving…" : "All changes saved."}
        </p>
      )}

      <div className="admin-card">
        <h3>Refresh Media</h3>
        <p className="admin-section-hint">
          Scan the folders below right now instead of waiting for the next
          check. New files are added, files that were deleted leave the guide,
          and the schedules are rebuilt. The server log records who started it.
        </p>
        <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
          <button
            className="admin-btn"
            onClick={() => void handleRefreshMedia()}
            disabled={refreshBusy}
          >
            {refreshBusy ? "Refreshing…" : "Refresh Media"}
          </button>
          <LastTime
            label="Last Refreshed"
            at={refreshTimes?.mediaRefreshedAt}
          />
        </div>
        {refreshError && <p className="profile-field-error">{refreshError}</p>}
        {refreshMessage && (
          <p className="admin-section-hint" role="status">
            {refreshMessage}
          </p>
        )}
      </div>

      <div className="admin-card">
        <h3>Polling Interval</h3>
        <p className="admin-section-hint">
          How often the server re-checks the folders below for new content, on
          top of the scan it always runs at startup. Finding new files
          automatically reclassifies genres/ratings and refreshes default
          channel schedules. A check never starts while another scan is still
          running.
        </p>
        {pollLoadError && (
          <p className="profile-field-error">{pollLoadError}</p>
        )}
        <div className="admin-field-row">
          <label>Check Every</label>
          <select
            value={pollInterval}
            onChange={(e) => changePollInterval(Number(e.target.value))}
            disabled={pollLoading}
          >
            {POLL_INTERVAL_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
        </div>
      </div>

      {saved && (
        <>
          <FolderListCard
            title="Movie Folders"
            placeholder="/path/to/movies"
            addLabel="+ Add folder"
            emptyText="No movie folders configured."
            draft={movieDraft}
            defaults={saved.movies.default}
            statuses={statusMap(saved.movies.paths)}
            counts={Object.fromEntries(
              saved.movies.paths.map((p) => [p.path, p.count ?? 0]),
            )}
            requireAbsolute
            onChange={changeMovies}
          />

          <FolderListCard
            title="TV Show Folders"
            placeholder="/path/to/tv-shows"
            addLabel="+ Add folder"
            emptyText="No TV folders configured."
            draft={tvDraft}
            defaults={saved.tv.default}
            statuses={statusMap(saved.tv.paths)}
            counts={Object.fromEntries(
              saved.tv.paths.map((p) => [p.path, p.count ?? 0]),
            )}
            requireAbsolute
            onChange={changeTv}
          />
        </>
      )}
      <CommercialsPage />
    </div>
  );
}
