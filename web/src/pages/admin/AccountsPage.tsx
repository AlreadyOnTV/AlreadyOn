// Owner-only management of the server's sign-in accounts. Owners see and do
// everything; viewers can only watch and manage their own account's profiles.
// Every account gets its own Admin, Teen and Kids profiles when it is created.
// The server refuses removing, disabling or demoting the last owner.
import { useCallback, useEffect, useState, type FormEvent } from 'react';
import {
  createAccount,
  deleteAccount,
  getAccounts,
  resetAccountPassword,
  updateAccount,
  type AccountRoleName,
  type ServerAccount,
} from '../../api/client';
import { PasswordInput } from '../../components/PasswordInput';
import { useAccount } from '../../users/AccountContext';
import SettingToggle from './SettingToggle';
import './AdminShared.css';

const MIN_PASSWORD = 4;
const streamsHelp = 'Limits how many devices this account can stream to at the same time. Set 0 for unlimited streams. A closed device frees its stream within five minutes.';
const passwordChangeHelp = 'Requires this account to choose a new password at its next sign-in before continuing. Enabling this turns off Password Locked.';
const passwordLockedHelp = 'Only a server owner can change this account’s password. Useful for shared demo accounts. Enabling this turns off Require Password Change.';
const MAX_STREAMS = 32;
const DEFAULT_VIEWER_STREAMS = 2;

const ROLE_LABELS: Record<AccountRoleName, string> = { owner: 'Owner', viewer: 'Viewer' };

interface Draft {
  username: string;
  password: string;
  role: AccountRoleName;
  mustChangePassword: boolean;
  passwordLocked: boolean;
  maxStreams: string;
}

const BLANK: Draft = { username: '', password: '', role: 'viewer', mustChangePassword: true, passwordLocked: false, maxStreams: String(DEFAULT_VIEWER_STREAMS) };

function streamsLabel(n: number): string {
  return n === 0 ? 'Unlimited' : String(n);
}

// Returns the validated stream count or an error message.
function parseStreams(text: string): number | string {
  const n = Number(text);
  if (text.trim() === '' || !Number.isInteger(n) || n < 0 || n > MAX_STREAMS) return `Simultaneous streams must be a whole number from 0 (unlimited) to ${MAX_STREAMS}.`;
  return n;
}

// One status per account: Disabled wins; a password-locked account shows
// Locked; Pending means the forced first password change is still outstanding.
function accountStatus(a: ServerAccount): 'Disabled' | 'Locked' | 'Pending' | 'Enabled' {
  if (a.disabled) return 'Disabled';
  if (a.passwordLocked) return 'Locked';
  if (a.mustChangePassword) return 'Pending';
  return 'Enabled';
}

export default function AccountsPage() {
  const { username: me } = useAccount();
  const [accounts, setAccounts] = useState<ServerAccount[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<number | 'new' | null>(null);
  const [resetting, setResetting] = useState<number | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<number | null>(null);

  const load = useCallback(() => {
    getAccounts()
      .then((list) => {
        setAccounts(list);
        setError(null);
      })
      .catch((err: Error) => setError(err.message));
  }, []);

  useEffect(load, [load]);

  // Successful changes show in the table itself, so only failures get a message.
  const run = async (action: () => Promise<unknown>) => {
    setError(null);
    try {
      await action();
      load();
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'The request failed.');
      return false;
    }
  };

  const remove = async (a: ServerAccount) => {
    if (confirmDelete !== a.id) {
      setConfirmDelete(a.id);
      return;
    }
    setConfirmDelete(null);
    await run(() => deleteAccount(a.id));
  };

  return (
    <div className="admin-section">
      <div className="admin-card">
        <div className="folder-card-header">
          <h3>Home Users</h3>
          <button className="admin-btn" onClick={() => setEditing('new')} style={{ visibility: editing === null ? 'visible' : 'hidden' }}>
            + Add Account
          </button>
        </div>
        <p className="admin-section-hint">
          Each account signs in with its own username and password and has its own profiles. Owners can see and change everything. Viewers can watch and
          manage only their own profiles. Simultaneous Streams limits how many devices can watch at once (0 is unlimited); a closed device frees its stream
          within five minutes.
        </p>
        {error && <p className="profile-field-error">{error}</p>}
        {editing === 'new' && (
          <AccountForm
            onCancel={() => setEditing(null)}
            onSave={async (draft, streams) => {
              const ok = await run(
                () =>
                  createAccount({
                    username: draft.username.trim(),
                    password: draft.password,
                    role: draft.role,
                    mustChangePassword: draft.mustChangePassword,
                    passwordLocked: draft.passwordLocked,
                    maxStreams: streams,
                  }),
              );
              if (ok) setEditing(null);
            }}
          />
        )}
        <div className="accounts-table-scroll" role="region" aria-label="Home Users table" tabIndex={0}>
        <table className="admin-table accounts-table">
          <thead>
            <tr>
              <th>Username</th>
              <th>Role</th>
              <th>Status</th>
              <th>Simultaneous Streams</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {accounts.map((a) =>
              editing === a.id ? (
                <tr key={a.id}>
                  <td colSpan={5}>
                    <AccountForm
                      account={a}
                      onCancel={() => setEditing(null)}
                      onSave={async (draft, streams) => {
                        const ok = await run(
                          () =>
                            updateAccount(a.id, {
                              username: draft.username.trim(),
                              role: draft.role,
                              mustChangePassword: draft.mustChangePassword,
                              passwordLocked: draft.passwordLocked,
                              maxStreams: streams,
                            }),
                        );
                        if (ok) setEditing(null);
                      }}
                    />
                  </td>
                </tr>
              ) : (
                <tr key={a.id}>
                  <td>
                    <span className="account-name-cell">
                      {a.username}
                      {a.username === me && <span className="admin-badge admin-badge-on">You</span>}
                    </span>
                  </td>
                  <td>{ROLE_LABELS[a.role]}</td>
                  <td>
                    <span className={'admin-badge' + (accountStatus(a) === 'Enabled' ? ' admin-badge-on' : '')}>{accountStatus(a)}</span>
                  </td>
                  <td>{streamsLabel(a.maxStreams)}</td>
                  <td className="admin-table-actions">
                    <button className="admin-btn admin-btn-secondary" onClick={() => setEditing(a.id)} disabled={editing !== null}>
                      Edit
                    </button>
                    <button className="admin-btn admin-btn-secondary" onClick={() => setResetting(resetting === a.id ? null : a.id)}>
                      Reset Password
                    </button>
                    {a.username !== me && (
                      <>
                        <button
                          className="admin-btn admin-btn-secondary"
                          onClick={() => void run(() => updateAccount(a.id, { disabled: !a.disabled }))}
                        >
                          {a.disabled ? 'Enable' : 'Disable'}
                        </button>
                        <button className={'admin-btn admin-btn-secondary' + (confirmDelete === a.id ? ' admin-btn-danger' : '')} onClick={() => void remove(a)}>
                          {confirmDelete === a.id ? 'Confirm: Delete Account And Profiles?' : 'Delete'}
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              ),
            )}
            {accounts.length === 0 && !error && (
              <tr>
                <td colSpan={5} className="admin-section-hint">
                  Loading…
                </td>
              </tr>
            )}
          </tbody>
        </table>
        </div>
        {resetting !== null && (
          <ResetPasswordForm
            account={accounts.find((a) => a.id === resetting)}
            onCancel={() => setResetting(null)}
            onSave={async (password, mustChange) => {
              const ok = await run(() => resetAccountPassword(resetting, password, mustChange));
              if (ok) setResetting(null);
            }}
          />
        )}
      </div>
    </div>
  );
}

function AccountForm({ account, onSave, onCancel }: { account?: ServerAccount; onSave: (draft: Draft, streams: number) => Promise<void>; onCancel: () => void }) {
  const creating = !account;
  const [draft, setDraft] = useState<Draft>(
    account
      ? { username: account.username, password: '', role: account.role, mustChangePassword: account.mustChangePassword, passwordLocked: account.passwordLocked, maxStreams: String(account.maxStreams) }
      : BLANK,
  );
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const set = (patch: Partial<Draft>) => setDraft((d) => ({ ...d, ...patch }));

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setError(null);
    if (!draft.username.trim()) return setError('Enter a username.');
    if (creating && Array.from(draft.password).length < MIN_PASSWORD) return setError(`The password needs at least ${MIN_PASSWORD} characters.`);
    const streams = parseStreams(draft.maxStreams);
    if (typeof streams === 'string') return setError(streams);
    setBusy(true);
    await onSave(draft, streams);
    setBusy(false);
  };

  return (
    <form className="account-form" onSubmit={(e) => void submit(e)}>
      <h4>{creating ? 'Add Account' : `Edit ${account.username}`}</h4>
      <div className="admin-field-row">
        <label htmlFor="acct-username">Username</label>
        <input id="acct-username" type="text" autoCapitalize="none" spellCheck={false} maxLength={64} value={draft.username} onChange={(e) => set({ username: e.target.value })} autoFocus />
      </div>
      {creating && (
        <div className="admin-field-row">
          <label htmlFor="acct-password">Initial Password</label>
          <PasswordInput id="acct-password" autoComplete="new-password" value={draft.password} onChange={(e) => set({ password: e.target.value })} />
        </div>
      )}
      <div className="admin-field-row">
        <label htmlFor="acct-role">Role</label>
        <select id="acct-role" value={draft.role} onChange={(e) => set({ role: e.target.value as AccountRoleName })}>
          <option value="viewer">Viewer</option>
          <option value="owner">Owner</option>
        </select>
      </div>
      <div className="admin-field-row">
        <label htmlFor="acct-streams" title={streamsHelp} style={{ cursor: 'help' }}>Simultaneous Streams</label>
        <input id="acct-streams" type="number" min={0} max={MAX_STREAMS} value={draft.maxStreams} onChange={(e) => set({ maxStreams: e.target.value })} />
      </div>
      <div className="admin-field-row">
        <label title={passwordChangeHelp} style={{ cursor: 'help' }}>Require Password Change</label>
        <SettingToggle checked={draft.mustChangePassword} onChange={(v) => set(v ? { mustChangePassword: true, passwordLocked: false } : { mustChangePassword: false })} label="Require Password Change" text />
      </div>
      <div className="admin-field-row">
        <label title={passwordLockedHelp} style={{ cursor: 'help' }}>Password Locked</label>
        <SettingToggle checked={draft.passwordLocked} onChange={(v) => set(v ? { passwordLocked: true, mustChangePassword: false } : { passwordLocked: false })} label="Password Locked" text />
      </div>
      {error && <p className="profile-field-error">{error}</p>}
      <div className="admin-table-actions account-form-actions">
        <button className="admin-btn" type="submit" disabled={busy}>
          {busy ? 'Saving…' : creating ? 'Create Account' : 'Save Account'}
        </button>
        <button className="admin-btn admin-btn-secondary" type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function ResetPasswordForm({ account, onSave, onCancel }: { account?: ServerAccount; onSave: (password: string, mustChange: boolean) => Promise<void>; onCancel: () => void }) {
  const [password, setPassword] = useState('');
  const [mustChange, setMustChange] = useState(!account?.passwordLocked);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  if (!account) return null;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setError(null);
    if (Array.from(password).length < MIN_PASSWORD) return setError(`The password needs at least ${MIN_PASSWORD} characters.`);
    setBusy(true);
    await onSave(password, mustChange && !account.passwordLocked);
    setBusy(false);
  };

  return (
    <form className="account-form" onSubmit={(e) => void submit(e)}>
      <h4>Reset Password For {account.username}</h4>
      <p className="admin-section-hint">The account is signed out of every browser and app.</p>
      <div className="admin-field-row">
        <label htmlFor="acct-reset">New Password</label>
        <PasswordInput id="acct-reset" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} autoFocus />
      </div>
      <div className="admin-field-row">
        <label title="Requires this account to choose a new password at its next sign-in before continuing. Unavailable while Password Locked is enabled." style={{ cursor: 'help' }}>Require Password Change</label>
        <SettingToggle checked={mustChange && !account.passwordLocked} onChange={(v) => setMustChange(v && !account.passwordLocked)} label="Require Password Change" text />
      </div>
      {account.passwordLocked && <p className="admin-section-hint">This account's password is locked, so it can't be required to change it.</p>}
      {error && <p className="profile-field-error">{error}</p>}
      <div className="admin-table-actions">
        <button className="admin-btn" type="submit" disabled={busy}>
          {busy ? 'Saving…' : 'Reset Password'}
        </button>
        <button className="admin-btn admin-btn-secondary" type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
