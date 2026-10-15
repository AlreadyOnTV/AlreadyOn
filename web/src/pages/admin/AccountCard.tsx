import SettingToggle from './SettingToggle';
import TvPairingField from './TvPairingField';
import { useTvPairing } from './useTvPairing';
// The signed-in account: change its password and manage which sessions are
// signed in, and rename it. A password change signs every other session out; sessions can also
// be ended one at a time, or all at once. The server-wide sign-in lifetime is
// shown to owners only, and a password-locked account sees no password form.
import { useCallback, useEffect, useRef, useState, type ReactNode, type FormEvent, type ChangeEvent } from 'react';
import {
  changePassword,
  changeUsername,
  getSessionLifetime,
  getSessions,
  revokeSession,
  revokeSessions,
  setSessionLifetime,
  type BrowserSession,
} from '../../api/client';
import { PasswordInput } from '../../components/PasswordInput';
import { useAccount } from '../../users/AccountContext';
import './AdminShared.css';

const MIN_PASSWORD = 4;

function describeAgent(ua: string): string {
  if (!ua) return 'Unknown Device';
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Browser';
  const os = /Windows/.test(ua)
    ? 'Windows'
    : /Android/.test(ua)
      ? 'Android'
      : /iPhone|iPad/.test(ua)
        ? 'iOS'
        : /Mac OS X/.test(ua)
          ? 'macOS'
          : /Linux/.test(ua)
            ? 'Linux'
            : 'unknown system';
  return `${browser} on ${os}`;
}

const when = (iso: string | null) => (iso === null ? 'Never' : new Date(iso).toLocaleString());

const DEFAULT_SESSION_DAYS = 30;
const MAX_SESSION_DAYS = 3650;
const LIFETIME_MESSAGE_MS = 5000;

export default function AccountCard({ children }: { children: ReactNode }) {
  const { username, isOwner, passwordLocked, rename } = useAccount();
  const pairing = useTvPairing();
  const [newName, setNewName] = useState(username);
  const renaming = newName.trim() !== username;
  // Fields start read-only so browser password managers (which ignore autocomplete="off")
  // cannot fill them on page load; the first focus unlocks them for typing.
  const [unlocked, setUnlocked] = useState(false);
  const [sessions, setSessions] = useState<BrowserSession[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<'all' | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [passwordMessage, setPasswordMessage] = useState<string | null>(null);
  const [passwordError, setPasswordError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // Days a session stays signed in (0 is never); applies to later sign-ins.
  const [savedDays, setSavedDays] = useState<number | null>(null);
  const [daysText, setDaysText] = useState(String(DEFAULT_SESSION_DAYS));
  const [never, setNever] = useState(false);
  const [lifetimeError, setLifetimeError] = useState<string | null>(null);
  const [lifetimeMessage, setLifetimeMessage] = useState<string | null>(null);
  const [lifetimeSaving, setLifetimeSaving] = useState(false);
  const lifetimeRequest = useRef(0);

  const load = useCallback(() => {
    getSessions()
      .then((list) => {
        setSessions(list);
        setError(null);
      })
      .catch((err: Error) => setError(err.message));
  }, []);

  useEffect(() => {
    if (!isOwner) {
      load();
      return;
    }
    getSessionLifetime()
      .then((days) => {
        setSavedDays(days);
        setNever(days === 0);
        setDaysText(String(days === 0 ? DEFAULT_SESSION_DAYS : days));
      })
      .catch((err: Error) => setLifetimeError(err.message));
    load();
  }, [isOwner, load]);

  const wantedDays = never ? 0 : Number(daysText);
  const daysValid = never || (Number.isInteger(wantedDays) && wantedDays >= 1 && wantedDays <= MAX_SESSION_DAYS);

  const saveLifetime = useCallback(async (days: number) => {
    const request = ++lifetimeRequest.current;
    setLifetimeError(null);
    setLifetimeMessage(null);
    setLifetimeSaving(true);
    try {
      const saved = await setSessionLifetime(days);
      if (request !== lifetimeRequest.current) return;
      setSavedDays(saved);
      setLifetimeMessage(days === 0 ? 'New sign-ins no longer expire.' : `New sign-ins now last ${days} ${days === 1 ? 'day' : 'days'}.`);
    } catch (err) {
      if (request === lifetimeRequest.current) setLifetimeError(err instanceof Error ? err.message : 'Failed to save the sign-in lifetime');
    } finally {
      if (request === lifetimeRequest.current) setLifetimeSaving(false);
    }
  }, []);

  // Number entry waits for a pause so typing 120 does not save 1, then 12,
  // then 120. The Never Expire switch bypasses this delay below.
  useEffect(() => {
    if (savedDays === null || never || lifetimeSaving || wantedDays === savedDays) return;
    if (!daysValid) return;
    const timer = window.setTimeout(() => void saveLifetime(wantedDays), 3000);
    return () => window.clearTimeout(timer);
  }, [daysText, daysValid, lifetimeSaving, never, savedDays, saveLifetime, wantedDays]);

  // Successful lifetime updates are transient confirmation, unlike validation
  // errors which remain until the administrator corrects the value.
  useEffect(() => {
    if (!lifetimeMessage) return;
    const timer = window.setTimeout(() => setLifetimeMessage(null), LIFETIME_MESSAGE_MS);
    return () => window.clearTimeout(timer);
  }, [lifetimeMessage]);

  const changeNever = (value: boolean) => {
    setNever(value);
    setLifetimeError(null);
    const finiteDays = Number(daysText);
    if (value) void saveLifetime(0);
    else if (Number.isInteger(finiteDays) && finiteDays >= 1 && finiteDays <= MAX_SESSION_DAYS) void saveLifetime(finiteDays);
  };

  // Password-manager extensions (Bitwarden) ignore every autocomplete hint and fill
  // these boxes by script, which raises untrusted input events. Ignoring them lets
  // React restore the controlled value, so only real typing or pasting changes it.
  const typed = (set: (value: string) => void) => (e: ChangeEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isTrusted) set(e.target.value);
  };

  // Save Changes applies the username and/or password change, both confirmed by the
  // current password; an empty New Password keeps the existing password.
  const submitChanges = async (event: FormEvent) => {
    event.preventDefault();
    setPasswordError(null);
    setPasswordMessage(null);
    const changingPassword = next !== '' || confirm !== '';
    if (!newName.trim()) return setPasswordError('Enter a username.');
    if (!renaming && !changingPassword && !pairing.pin) return setPasswordError('There are no changes to save.');
    if ((renaming || changingPassword) && !current) return setPasswordError('Enter your current password to save changes.');
    if (changingPassword) {
      if (Array.from(next).length < MIN_PASSWORD) return setPasswordError(`The new password needs at least ${MIN_PASSWORD} characters.`);
      if (next !== confirm) return setPasswordError("The new passwords don't match.");
    }
    setSaving(true);
    const done: string[] = [];
    try {
      if (renaming) {
        const saved = await changeUsername(current, newName.trim());
        rename?.(saved);
        setNewName(saved);
        done.push('Username changed.');
      }
      if (changingPassword) {
        await changePassword(current, next);
        setNext('');
        setConfirm('');
        done.push('Password changed. Every other session was signed out.');
        load();
      }
      if (pairing.pin) { await pairing.save(); done.push('Changes saved.'); }
      setCurrent('');
      setPasswordMessage(done.join(' '));
    } catch (err) {
      if (done.length) setPasswordMessage(done.join(' '));
      setPasswordError(err instanceof Error ? err.message : 'Failed to save changes');
    } finally {
      setSaving(false);
    }
  };

  const signOutOne = async (s: BrowserSession) => {
    setMessage(null);
    try {
      await revokeSession(s.id);
      if (s.current) window.location.assign('/');
      else load();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to sign that session out');
    }
  };

  const signOutOthers = async () => {
    setMessage(null);
    try {
      const n = await revokeSessions('others');
      setMessage(n === 0 ? 'No other sessions were signed in.' : `Signed out ${n} other ${n === 1 ? 'session' : 'sessions'}.`);
      load();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to sign sessions out');
    }
  };

  const signOutEverywhere = async () => {
    if (confirming !== 'all') {
      setConfirming('all');
      return;
    }
    try {
      await revokeSessions('all');
      window.location.assign('/');
    } catch (err) {
      setConfirming(null);
      setError(err instanceof Error ? err.message : 'Failed to sign sessions out');
    }
  };

  const others = sessions.filter((s) => !s.current).length;

  return (
    <>
    <div className="admin-card account-card">
      <form autoComplete="off" onSubmit={(e) => void submitChanges(e)}>
      {passwordLocked ? (
        <>
          <div className="admin-field-row">
            <label htmlFor="account-username">Username</label>
            <input id="account-username" type="text" value={username} readOnly />
          </div>
          <p className="admin-section-hint">This account's password is locked. Only a server owner can change it.</p>
        </>
      ) : (
      <>
        <div className="admin-field-row">
          <label htmlFor="account-current">Current Password</label>
          <PasswordInput concealFromManagers id="account-current" name="account-current" value={current} readOnly={!unlocked} onFocus={() => setUnlocked(true)} onChange={typed(setCurrent)} />
        </div>
        <div className="admin-field-row">
          <label htmlFor="account-username">Username</label>
          <input
            id="account-username"
            type="text"
            name="account-username"
            autoComplete="off"
            data-lpignore="true"
            data-1p-ignore="true"
            data-bwignore="true"
            data-form-type="other"
            maxLength={64}
            value={newName}
            readOnly={!unlocked}
            onFocus={() => setUnlocked(true)}
            onChange={typed(setNewName)}
          />
        </div>
        <div className="admin-field-row">
          <label htmlFor="account-new">New Password</label>
          <PasswordInput concealFromManagers id="account-new" name="account-new" placeholder="Leave empty to keep current password" value={next} readOnly={!unlocked} onFocus={() => setUnlocked(true)} onChange={typed(setNext)} />
        </div>
        <div className="admin-field-row">
          <label htmlFor="account-confirm">Confirm New Password</label>
          <PasswordInput concealFromManagers id="account-confirm" name="account-confirm" placeholder="Leave empty to keep current password" value={confirm} readOnly={!unlocked} onFocus={() => setUnlocked(true)} onChange={typed(setConfirm)} />
        </div>
      </>
      )}
        <TvPairingField pairing={pairing} disabled={saving} />
        <div className="account-save-row">
          <button className="admin-btn" type="submit" disabled={saving || pairing.busy}>
            {saving ? 'Saving…' : 'Save Changes'}
          </button>
          {passwordError && <p className="profile-field-error" role="alert">{passwordError}</p>}
        </div>
      </form>
      {passwordMessage && <p className="admin-section-hint" role="status">{passwordMessage}</p>}

    </div>
    {children}
    <div className="admin-card account-card">
      <h3>Sessions</h3>
      {message && <p className="admin-section-hint" role="status">{message}</p>}
      {isOwner && (
      <div className="admin-field-row session-lifetime-row">
        <label htmlFor="account-days">Stay Signed In For (Days)</label>
        <input
          id="account-days"
          type="number"
          min={1}
          max={MAX_SESSION_DAYS}
          value={daysText}
          disabled={never}
          onChange={(e) => {
            const text = e.target.value;
            const days = Number(text);
            setDaysText(text);
            setLifetimeMessage(null);
            setLifetimeError(text !== '' && Number.isInteger(days) && days >= 1 && days <= MAX_SESSION_DAYS ? null : `Enter a whole number from 1 to ${MAX_SESSION_DAYS}.`);
          }}
          style={{ width: 100 }}
        />
        <div className="session-never-control">
          <label className="session-never-label">
            <SettingToggle checked={never} onChange={changeNever} label="Never Expire" text />
            <span>Never Expire</span>
          </label>
          {lifetimeSaving && <p className="session-lifetime-saving" role="status">Saving sign-in lifetime…</p>}
          {lifetimeMessage && <p className="session-lifetime-message" role="status">{lifetimeMessage}</p>}
        </div>
      </div>
      )}
      {isOwner && lifetimeError && <p className="profile-field-error">{lifetimeError}</p>}
      {error && <p className="profile-field-error">{error}</p>}
      <div className="sessions-table-scroll" role="region" aria-label="Sessions" tabIndex={0}>
      <table className="admin-table sessions-table">
        <thead>
          <tr>
            <th>Device</th>
            <th>Type</th>
            <th title="Last address seen by the server; a reverse proxy may supply its own address.">IP Address</th>
            <th>Last Active</th>
            <th>Expires</th>
            <th>Sign Out</th>
          </tr>
        </thead>
        <tbody>
          {sessions.map((s) => (
            <tr key={s.id}>
              <td>
                {s.deviceName || describeAgent(s.userAgent)}
                {s.current && <span className="admin-badge admin-badge-on session-current-device">This Session</span>}
              </td>
              <td>{s.deviceType || 'Web Browser'}</td>
              <td>{s.ipAddress || 'Not recorded yet'}</td>
              <td>{s.lastActiveAt ? when(s.lastActiveAt) : "Not recorded yet"}</td>
              <td>{when(s.expiresAt)}</td>
              <td className="admin-table-actions">
                <button className="admin-btn admin-btn-secondary" onClick={() => void signOutOne(s)}>
                  Sign Out
                </button>
              </td>
            </tr>
          ))}
          {sessions.length === 0 && !error && (
            <tr>
              <td colSpan={6} className="admin-section-hint">
                Loading…
              </td>
            </tr>
          )}
        </tbody>
      </table>
      </div>
      <div className="admin-table-actions session-browser-actions" style={{ marginTop: 12 }}>
        <button className="admin-btn admin-btn-secondary" onClick={() => void signOutOthers()} disabled={others === 0}>
          Sign Out All Other Sessions
        </button>
        <button className={'admin-btn admin-btn-secondary' + (confirming === 'all' ? ' admin-btn-danger' : '')} onClick={() => void signOutEverywhere()}>
          {confirming === 'all' ? 'Confirm: Sign Out Everywhere?' : 'Sign Out Everywhere'}
        </button>
      </div>
    </div>
    </>
  );
}
