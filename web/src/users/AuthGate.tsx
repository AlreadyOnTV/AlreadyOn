import Brand from '../components/Brand';
// Nothing in the app renders until this browser is signed in. The first visit
// creates the server's first owner account (centered setup dialog); owners
// add further accounts later. An account an owner created with "Require
// Password Change" must choose a new password before anything else works.
// The session lives in an HttpOnly cookie, so nothing secret is kept in page
// memory or web storage.
import { useCallback, useEffect, useMemo, useState, type FormEvent, type ReactNode } from 'react';
import { watchCapabilities } from '../playback/capabilities';
import { PasswordInput } from '../components/PasswordInput';
import { AccountContext, type Account, type AccountRole } from './AccountContext';
import './AuthGate.css';
import { safeNext } from './safeNext';

type Phase = 'loading' | 'unreachable' | 'setup' | 'login' | 'change-password' | 'ready';

interface AuthStatus {
  setupRequired: boolean;
  authenticated: boolean;
  username?: string;
  role?: AccountRole;
  mustChangePassword?: boolean;
  passwordLocked?: boolean;
}

const MIN_PASSWORD = 4;

async function post(path: string, body: unknown): Promise<{ ok: boolean; message: string }> {
  try {
    const res = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    if (res.ok) return { ok: true, message: '' };
    const data = await res.json().catch(() => ({}));
    return { ok: false, message: data.error || `The server refused the request (${res.status}).` };
  } catch {
    return { ok: false, message: 'The server could not be reached.' };
  }
}

export function AuthGate({ children }: { children: ReactNode }) {
  const [phase, setPhase] = useState<Phase>('loading');
  const [notice, setNotice] = useState('');
  const [account, setAccount] = useState<Account>({ username: '', role: 'viewer', passwordLocked: false });

  const check = useCallback(async () => {
    try {
      const res = await fetch('/api/auth/status', { cache: 'no-store' });
      if (!res.ok) throw new Error();
      const status: AuthStatus = await res.json();
      if (status.authenticated) {
        setAccount({ username: status.username ?? '', role: status.role === 'owner' ? 'owner' : 'viewer', passwordLocked: !!status.passwordLocked });
      }
      setPhase(status.setupRequired ? 'setup' : !status.authenticated ? 'login' : status.mustChangePassword ? 'change-password' : 'ready');
    } catch {
      setPhase('unreachable');
    }
  }, []);

  useEffect(() => {
    void check();
  }, [check]);

  const accountValue = useMemo<Account>(() => ({ ...account, rename: (username: string) => setAccount((a) => ({ ...a, username })) }), [account]);

  useEffect(() => {
    const ended = () => {
      setNotice('Your session ended. Sign in again.');
      setPhase('login');
    };
    // An owner can require a new password while this browser is signed in.
    const mustChange = () => void check();
    window.addEventListener('alreadyon-access-expired', ended);
    window.addEventListener('alreadyon-password-change-required', mustChange);
    return () => {
      window.removeEventListener('alreadyon-access-expired', ended);
      window.removeEventListener('alreadyon-password-change-required', mustChange);
    };
  }, [check]);

  useEffect(() => {
    if (phase !== 'ready') return;
    const next = safeNext(window.location.search, window.location.origin);
    if (next) {
      window.location.replace(next);
      return;
    }
    return watchCapabilities();
  }, [phase]);

  if (phase === 'ready') return <AccountContext.Provider value={accountValue}>{children}</AccountContext.Provider>;
  if (phase === 'loading') return <div className="auth-gate" role="status" />;
  if (phase === 'unreachable') {
    return (
      <div className="auth-gate">
        <div className="auth-dialog" role="alert">
          <Brand tagline />
          <h1>Can't Reach The Server</h1>
          <p className="auth-hint">The server didn't answer. Check that it is running, then try again.</p>
          <button className="auth-submit" onClick={() => void check()}>
            Try Again
          </button>
        </div>
      </div>
    );
  }
  if (phase === 'change-password') return <ChangePasswordDialog username={account.username} onDone={() => void check()} />;
  return <CredentialsDialog mode={phase} notice={notice} onDone={() => void check()} />;
}

// Shown after signing in to an account an owner created or reset with
// "Require Password Change". Nothing else works until the password changes;
// the server keeps this browser signed in and signs every other one out.
function ChangePasswordDialog({ username, onDone }: { username: string; onDone: () => void }) {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError('');
    if (Array.from(next).length < MIN_PASSWORD) return setError(`The new password needs at least ${MIN_PASSWORD} characters.`);
    if (next !== confirm) return setError("The new passwords don't match.");
    if (next === current) return setError('Choose a password different from the current one.');
    setBusy(true);
    const result = await post('/api/auth/password', { currentPassword: current, newPassword: next });
    setBusy(false);
    if (result.ok) onDone();
    else setError(result.message);
  }

  return (
    <div className="auth-gate">
      <form className="auth-dialog" role="dialog" aria-modal="true" aria-labelledby="auth-title" onSubmit={(e) => void submit(e)}>
        <Brand tagline />
        <h1 id="auth-title">Change Your Password</h1>
        <p className="auth-hint">
          {username ? <>The account <strong>{username}</strong> must</> : 'This account must'} choose a new password before continuing.
        </p>
        <input type="text" name="username" autoComplete="username" value={username} readOnly hidden />
        <label htmlFor="auth-current">Current Password</label>
        <PasswordInput id="auth-current" name="currentPassword" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required autoFocus />
        <label htmlFor="auth-new">New Password</label>
        <PasswordInput id="auth-new" name="newPassword" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} required />
        <label htmlFor="auth-confirm">Confirm New Password</label>
        <PasswordInput id="auth-confirm" name="confirmPassword" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
        {error && (
          <p className="auth-error" role="alert">
            {error}
          </p>
        )}
        <button className="auth-submit" type="submit" disabled={busy}>
          {busy ? 'Please Wait…' : 'Change Password And Continue'}
        </button>
        <button className="auth-link" type="button" onClick={() => void signOutHere()}>
          Sign Out
        </button>
      </form>
    </div>
  );
}

async function signOutHere() {
  await fetch('/api/auth/logout', { method: 'POST' }).catch(() => undefined);
  window.location.assign('/');
}

function CredentialsDialog({ mode, notice, onDone }: { mode: 'setup' | 'login'; notice: string; onDone: () => void }) {
  const setup = mode === 'setup';
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [serverName, setServerName] = useState('AlreadyOn');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [forgot, setForgot] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError('');
    if (setup) {
      if (Array.from(password).length < MIN_PASSWORD) return setError(`The password needs at least ${MIN_PASSWORD} characters.`);
      if (password !== confirm) return setError("The passwords don't match.");
    }
    setBusy(true);
    const result = await post(setup ? '/api/auth/setup' : '/api/auth/login', { username, password, ...(setup ? { serverName: serverName.trim() } : {}) });
    setBusy(false);
    if (result.ok) onDone();
    else setError(result.message);
  }

  return (
    <div className="auth-gate">
      <form className="auth-dialog" data-password-manager-login={!setup ? 'true' : undefined} role="dialog" aria-modal="true" aria-label={setup ? "Create Your AlreadyOn Account" : "Sign In"} onSubmit={(e) => void submit(e)}>
        <Brand tagline />
        {setup && <h1 id="auth-title">Create Your AlreadyOn Account</h1>}
        {setup ? (
          <p className="auth-hint">
            This server has no account yet. Choose the username and password of its first owner account. Owners can add accounts for other people later
            in Server Settings; each account has its own profiles.
          </p>
        ) : (
          notice && <p className="auth-hint">{notice}</p>
        )}
        {setup && <>
          <label htmlFor="auth-server-name">Server Name</label>
          <input id="auth-server-name" name="serverName" type="text" value={serverName} onChange={(e) => setServerName(e.target.value)} maxLength={64} required />
        </>}
        <label htmlFor="auth-username">Username</label>
        <input id="auth-username" name="username" type="text" autoComplete="username" autoCapitalize="none" autoCorrect="off" spellCheck={false} value={username} onChange={(e) => setUsername(e.target.value)} maxLength={64} required autoFocus />
        <label htmlFor="auth-password">Password</label>
        <PasswordInput
          allowAutofill={!setup}
          id="auth-password"
          name="password"
          autoComplete={setup ? 'new-password' : 'current-password'}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
        />
        {setup && (
          <>
            <label htmlFor="auth-confirm">Confirm Password</label>
            <PasswordInput id="auth-confirm" name="confirmPassword" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
          </>
        )}
        {error && (
          <p className="auth-error" role="alert">
            {error}
          </p>
        )}
        <button className="auth-submit" type="submit" disabled={busy}>
          {busy ? 'Please Wait…' : setup ? 'Create Account And Sign In' : 'Sign In'}
        </button>
        {!setup && (
          <>
            <button className="auth-link" type="button" aria-expanded={forgot} onClick={() => setForgot((f) => !f)}>
              Forgot Your Password?
            </button>
            {forgot && (
              <div className="auth-forgot">
                <p>Another server owner can reset your password for you. Alternatively, open a shell in the server’s Docker container:</p>
                <code>docker exec -it &lt;container_id&gt; /bin/bash</code>
                <p>Then run the reset tool and follow the prompts:</p>
                <code>alreadyon-reset-password</code>
                <p>
                  The reset tool signs out browsers. Come back here and sign in with the new password.
                </p>
              </div>
            )}
          </>
        )}
      </form>
    </div>
  );
}
