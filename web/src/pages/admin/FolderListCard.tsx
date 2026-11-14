import CustomizePathDialog from './CustomizePathDialog';
import type { LibraryPathKind } from './channelPaths';
import FolderBrowser from "./FolderBrowser";
import { useState } from "react";
import type { FolderPathStatus } from "../../api/client";
import { draftValues, normalizeDraft, type ListDraft } from "./folderDraft";

const STATUS_LABELS: Record<FolderPathStatus, string> = {
  ok: "Found",
  notFound: "Not Found",
  notDirectory: "Not A Folder",
  unreadable: "Not Readable",
  notAbsolute: "Must be an absolute path",
};

// An editable list of folders (or exclusion patterns) that is either the
// server's built-in default or a custom list the admin saved. Editing a
// default list turns it into a custom one; editing it back to exactly the
// defaults (or "Reset To Default") makes it a default again.
export default function FolderListCard({
  title,
  hint,
  placeholder,
  addLabel,
  emptyText,
  draft,
  defaults,
  statuses,
  counts,
  requireAbsolute,
  disabled = false,
  onChange,
  customizeKind,
}: {
  title: string;
  hint?: string;
  placeholder: string;
  addLabel: string;
  emptyText: string;
  draft: ListDraft;
  defaults: string[];
  // Live status of the folders as last saved on the server; unsaved
  // entries have none.
  statuses?: Record<string, FolderPathStatus>;
  counts?: Record<string, number>;
  requireAbsolute?: boolean;
  disabled?: boolean;
  onChange: (next: ListDraft) => void;
  customizeKind?: LibraryPathKind;
}) {
  const [customizing, setCustomizing] = useState<string | null>(null);
  const [browsing, setBrowsing] = useState(false);
  const [text, setText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const values = draftValues(draft, defaults);
  const isDefault = draft === null;

  const handleAdd = () => {
    const trimmed = text.trim();
    if (trimmed === "") return;
    if (requireAbsolute && !trimmed.startsWith("/")) {
      setError("Enter an absolute path, starting with /");
      return;
    }
    setError(null);
    setText("");
    if (!values.includes(trimmed)) onChange(normalizeDraft([...values, trimmed], defaults));
  };

  return (
    <div className="admin-card">
      <div className="folder-card-header">
        <h3>{title}</h3>
        <span className={"admin-badge" + (isDefault ? "" : " admin-badge-on")}>{isDefault ? "Default" : "Custom"}</span>
        {!isDefault && (
          <button disabled={disabled} className="admin-btn admin-btn-secondary" onClick={() => onChange(null)}>
            Reset To Default
          </button>
        )}
      </div>
      {hint && <p className="admin-section-hint">{hint}</p>}
      {values.length === 0 ? (
        <p className="admin-section-hint">{emptyText}</p>
      ) : (
        values.map((v) => {
          const status = statuses?.[v];
          return (
            <div className="admin-list-item folder-list-item" key={v}>
              <span className="folder-path">
                {v}{status === "ok" && counts && <span className="folder-status"> (Found {counts[v] ?? 0})</span>}
                {status && (status !== "ok" || !counts) && (
                  <span className={"folder-status" + (status === "ok" ? "" : " folder-status-problem")}>
                    {status === "ok" ? " " : " ⚠ "}{STATUS_LABELS[status]}
                  </span>
                )}
              </span>
              <span className="folder-item-actions">
                {customizeKind && (
                  <button type="button" disabled={disabled || !status} className="admin-btn admin-btn-secondary"
                    aria-label={`Customize ${v}`} onClick={() => setCustomizing(v)}>Customize</button>
                )}
                <button disabled={disabled}
                  className="admin-btn admin-btn-secondary"
                  onClick={() => onChange(normalizeDraft(values.filter((existing) => existing !== v), defaults))}
                >
                  Remove
                </button>
              </span>
            </div>
          );
        })
      )}
      <div className="admin-field-row" style={{ marginTop: 12 }}>
        {!requireAbsolute && <input
          type="text"
          value={text}
          placeholder={placeholder}
          onChange={(e) => {
            setText(e.target.value);
            setError(null);
          }}
          onKeyDown={(e) => e.key === "Enter" && handleAdd()}
        />}
        <button disabled={disabled} className="admin-btn" onClick={() => requireAbsolute ? setBrowsing(true) : handleAdd()}>
          {addLabel}
        </button>
      </div>
      {customizing && customizeKind && <CustomizePathDialog path={customizing} kind={customizeKind} onClose={() => setCustomizing(null)} />}
      {browsing && <FolderBrowser title={title} onClose={() => setBrowsing(false)} onSelect={(path) => {
        if (!values.includes(path)) onChange(normalizeDraft([...values, path], defaults));
        setBrowsing(false);
      }} />}
      {error && <p className="profile-field-error">{error}</p>}
    </div>
  );
}
