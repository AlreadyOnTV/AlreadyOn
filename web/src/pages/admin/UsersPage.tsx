// Profiles are stored on the server and shared with the page-header profile
// menu via UsersContext, so adding/editing/deleting here shows up there and
// on every other device.
import { useState } from 'react';
import { useUsers, AUDIENCE_TIER_OPTIONS, audienceTierLabel, roleLabel, type UserRecord, type UserDraft } from '../../users/UsersContext';
import AccountCard from './AccountCard';
import { CheckIcon, PencilIcon, TrashIcon } from './AdminIcons';
import './AdminShared.css';

const BLANK_DRAFT: UserDraft = { name: '', role: 'viewer', audienceTier: 'adult' };

type SortKey = 'name' | 'role' | 'audienceTier' | 'pin';
type SortDir = 'asc' | 'desc';

const COLUMNS: { key: SortKey; label: string }[] = [
  { key: 'name', label: 'Name' },
  { key: 'role', label: 'Role' },
  { key: 'audienceTier', label: 'Audience' },
  { key: 'pin', label: 'PIN' },
];

export default function UsersPage() {
  const { users, addUser, updateUser, deleteUser, clearPin } = useUsers();
  const [editingId, setEditingId] = useState<number | 'new' | null>(null);
  const [draft, setDraft] = useState<UserDraft>(BLANK_DRAFT);
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);
  const [confirmResetPinId, setConfirmResetPinId] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sortKey, setSortKey] = useState<SortKey>('name');
  const [sortDir, setSortDir] = useState<SortDir>('asc');

  const handleSort = (key: SortKey) => {
    if (key === sortKey) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'));
    } else {
      setSortKey(key);
      setSortDir('asc');
    }
  };

  const sortedUsers = [...users].sort((a, b) => {
    let cmp = 0;
    switch (sortKey) {
      case 'name':
        cmp = a.name.localeCompare(b.name);
        break;
      case 'role':
        cmp = a.role.localeCompare(b.role);
        break;
      case 'audienceTier':
        cmp = a.audienceTier.localeCompare(b.audienceTier);
        break;
      case 'pin':
        cmp = Number(a.hasPin) - Number(b.hasPin);
        break;
    }
    return sortDir === 'asc' ? cmp : -cmp;
  });

  const startAdd = () => {
    setDraft(BLANK_DRAFT);
    setEditingId('new');
    setConfirmDeleteId(null);
    setError(null);
  };

  const startEdit = (user: UserRecord) => {
    setDraft({ name: user.name, role: user.role, audienceTier: user.audienceTier });
    setEditingId(user.id);
    setConfirmDeleteId(null);
    setError(null);
  };

  const cancelEdit = () => setEditingId(null);

  // Runs a server call and shows its message (for example "there must be at
  // least one admin profile") instead of failing silently.
  const attempt = async (action: () => Promise<unknown>): Promise<boolean> => {
    setError(null);
    try {
      await action();
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Something went wrong');
      return false;
    }
  };

  const saveEdit = async () => {
    const name = draft.name.trim();
    if (!name) return;
    const toSave: UserDraft = { name, role: draft.role, audienceTier: draft.audienceTier };
    const saved = await attempt(() => (editingId === 'new' ? addUser(toSave) : updateUser(editingId!, toSave)));
    if (saved) setEditingId(null);
  };

  const handleDeleteClick = async (id: number) => {
    if (confirmDeleteId !== id) {
      setConfirmDeleteId(id);
      return;
    }
    setConfirmDeleteId(null);
    if ((await attempt(() => deleteUser(id))) && editingId === id) setEditingId(null);
  };

  const handleResetPinClick = async (id: number) => {
    if (confirmResetPinId !== id) {
      setConfirmResetPinId(id);
      return;
    }
    setConfirmResetPinId(null);
    await attempt(() => clearPin(id));
  };

  return (
    <div className="admin-section">
      <AccountCard>
      {error && <p className="profile-field-error">{error}</p>}
      <div className="admin-card">
        <div className="folder-card-header">
          <h3>Profiles</h3>
          <button className="admin-btn" onClick={startAdd} style={{ visibility: editingId === null ? 'visible' : 'hidden' }}>
            + Add Profile
          </button>
        </div>
        <div className="profiles-table-scroll" role="region" aria-label="Profiles table" tabIndex={0}>
        <table className="admin-table profiles-table">
          <thead>
            <tr>
              <th>Actions</th>
              {COLUMNS.map((col) => (
                <th key={col.key} className="admin-table-sortable" onClick={() => handleSort(col.key)}>
                  {col.label}
                  {sortKey === col.key && <span className="admin-sort-arrow">{sortDir === 'asc' ? ' ▲' : ' ▼'}</span>}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {sortedUsers.map((u) =>
              editingId === u.id ? (
                <UserEditRow key={u.id} draft={draft} setDraft={setDraft} onSave={saveEdit} onCancel={cancelEdit} />
              ) : (
                <tr key={u.id}>
                  <td className="admin-table-actions profile-row-actions">
                    <button className="admin-icon-btn" aria-label={`Edit ${u.name}`} title="Edit profile" onClick={() => startEdit(u)}>
                      <PencilIcon />
                    </button>
                    <button
                      className={'admin-icon-btn' + (confirmDeleteId === u.id ? ' admin-btn-danger' : '')}
                      aria-label={confirmDeleteId === u.id ? `Confirm delete ${u.name}` : `Delete ${u.name}`}
                      title={confirmDeleteId === u.id ? 'Confirm delete profile' : 'Delete profile'}
                      onClick={() => handleDeleteClick(u.id)}
                    >
                      <TrashIcon />
                    </button>
                    {u.hasPin && (
                      <button
                        className={'admin-btn admin-btn-secondary profile-reset-pin' + (confirmResetPinId === u.id ? ' admin-btn-danger' : '')}
                        onClick={() => handleResetPinClick(u.id)}
                      >
                        {confirmResetPinId === u.id ? 'Confirm reset?' : 'Reset PIN'}
                      </button>
                    )}
                  </td>
                  <td>{u.name}</td>
                  <td>{roleLabel(u.role)}</td>
                  <td>{audienceTierLabel(u.audienceTier)}</td>
                  <td>
                    <span className={'admin-badge' + (u.hasPin ? ' admin-badge-on' : '')}>{u.hasPin ? 'Set' : 'None'}</span>
                  </td>
                </tr>
              ),
            )}
            {editingId === 'new' && <UserEditRow draft={draft} setDraft={setDraft} onSave={saveEdit} onCancel={cancelEdit} />}
          </tbody>
        </table>
        </div>
      </div>

      </AccountCard>
    </div>
  );
}

function UserEditRow({ draft, setDraft, onSave, onCancel }: { draft: UserDraft; setDraft: (d: UserDraft) => void; onSave: () => void; onCancel: () => void }) {
  return (
    <tr>
      <td className="admin-table-actions profile-row-actions">
        <button className="admin-icon-btn admin-icon-btn-primary" aria-label="Save profile" title="Save profile" onClick={onSave}>
          <CheckIcon />
        </button>
        <button className="admin-icon-btn" aria-label="Cancel profile edit" title="Cancel profile edit" onClick={onCancel}>
          <span className="admin-cancel-icon" aria-hidden="true">X</span>
        </button>
      </td>
      <td>
        <input
          type="text"
          value={draft.name}
          placeholder="Name"
          autoFocus
          onChange={(e) => setDraft({ ...draft, name: e.target.value })}
          onKeyDown={(e) => e.key === 'Enter' && onSave()}
        />
      </td>
      <td>
        <select className="admin-select-wide" value={draft.role} onChange={(e) => setDraft({ ...draft, role: e.target.value as UserRecord['role'] })}>
          <option value="admin">Admin</option>
          <option value="viewer">Viewer</option>
        </select>
      </td>
      <td>
        <select value={draft.audienceTier} onChange={(e) => setDraft({ ...draft, audienceTier: e.target.value as UserRecord['audienceTier'] })}>
          {AUDIENCE_TIER_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
      </td>
      <td></td>
    </tr>
  );
}
