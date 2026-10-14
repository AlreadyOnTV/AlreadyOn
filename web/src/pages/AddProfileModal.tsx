import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { createPortal } from 'react-dom';
import { AUDIENCE_TIER_OPTIONS, useUsers, type UserDraft } from '../users/UsersContext';
import './AddProfileModal.css';

// Native modal dialogs center in the viewport, trap keyboard focus and keep
// profile creation on the existing authenticated, account-scoped API.
export default function AddProfileModal({ onClose }: { onClose: () => void }) {
  const { addUser } = useUsers();
  const dialog = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const nameId = useId();
  const roleId = useId();
  const audienceId = useId();
  const [draft, setDraft] = useState<UserDraft>({ name: '', role: 'viewer', audienceTier: 'adult' });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    const element = dialog.current!;
    element.showModal();
    return () => element.close();
  }, []);
  const dismiss = () => {
    // Release the native focus trap before restoring the header control's focus.
    dialog.current?.close();
    onClose();
  };
  const create = async (event: FormEvent) => {
    event.preventDefault();
    if (busy || !draft.name.trim()) return;
    setBusy(true);
    setError(null);
    try {
      await addUser({ ...draft, name: draft.name.trim() });
      dismiss();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Could not create profile.');
      setBusy(false);
    }
  };
  return createPortal(<dialog ref={dialog} className="profile-create-modal" aria-labelledby={titleId}
    onCancel={event => { event.preventDefault(); if (!busy) dismiss(); }}>
    <h2 id={titleId}>Add Profile</h2>
    <form onSubmit={event => void create(event)}>
      <label htmlFor={nameId}>Name</label>
      <input id={nameId} value={draft.name} onChange={event => setDraft({ ...draft, name: event.target.value })} required autoFocus disabled={busy} />
      <label htmlFor={roleId}>Role</label>
      <select id={roleId} value={draft.role} onChange={event => setDraft({ ...draft, role: event.target.value as UserDraft['role'] })} disabled={busy}>
        <option value="admin">Admin</option><option value="viewer">Viewer</option>
      </select>
      <label htmlFor={audienceId}>Audience</label>
      <select id={audienceId} value={draft.audienceTier} onChange={event => setDraft({ ...draft, audienceTier: event.target.value as UserDraft['audienceTier'] })} disabled={busy}>
        {[...AUDIENCE_TIER_OPTIONS].sort((a, b) => a.label.localeCompare(b.label)).map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
      {error && <p role="alert">{error}</p>}
      <div className="profile-create-actions">
        <button type="submit" disabled={busy || !draft.name.trim()}>{busy ? 'Creating…' : 'Create'}</button>
        <button type="button" onClick={dismiss} disabled={busy}>Cancel</button>
      </div>
    </form>
  </dialog>, document.body);
}
