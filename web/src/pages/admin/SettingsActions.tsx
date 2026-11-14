// Shared Save Changes / Cancel bar for Admin settings tabs that hold a
// page-level draft (Server) — Library Paths autosaves; Users and Channels
// already commit each row immediately via their own inline Save/Cancel,
// so they don't use this. Save commits the draft (via onSave, which may
// hit a real API) and then reloads the page so what's on screen is
// exactly what's now persisted; Cancel just reloads without saving,
// which is enough to discard any in-memory draft and show the
// last-saved values again.
import { useState } from "react";
import { reloadKeepingQuality } from "../../users/deviceStorage";

export default function SettingsActions({
  hasChanges,
  onSave,
}: {
  hasChanges: boolean;
  onSave: () => void | Promise<void>;
}) {
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSave = async () => {
    setSaving(true);
    setError(null);
    try {
      await onSave();
      reloadKeepingQuality();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save changes");
      setSaving(false);
    }
  };

  return (
    <>
      {error && <p className="profile-field-error">{error}</p>}
      <div className="admin-table-actions">
        <button className="admin-btn" disabled={!hasChanges || saving} onClick={handleSave}>
          {saving ? "Saving…" : "Save Changes"}
        </button>
        <button className="admin-btn admin-btn-secondary" onClick={reloadKeepingQuality}>
          Cancel
        </button>
      </div>
    </>
  );
}
