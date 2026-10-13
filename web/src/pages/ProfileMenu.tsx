// Dashboard, profile settings and switching are available to every profile.
// Web-only administration follows, then Sign Out on every device.
import { startTransition, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useUsers, roleLabel } from '../users/UsersContext';
import { useAccount } from '../users/AccountContext';
import { PasswordInput } from '../components/PasswordInput';
import { UserAvatar } from '../users/avatars';
import { signOut } from '../api/access';
import './ProfileMenu.css';

type MenuView = 'root' | 'switch' | 'pin';

export default function ProfileMenu() {
  const { users, currentUser, isAdmin, setCurrentUserId, verifyPin } = useUsers();
  const { isOwner } = useAccount();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<MenuView>('root');
  const [pendingUserId, setPendingUserId] = useState<number | null>(null);
  const [pinInput, setPinInput] = useState('');
  const [pinError, setPinError] = useState<string | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const go = (path: string) => {
    setOpen(false);
    navigate(path);
  };

  useEffect(() => {
    if (!open) return;
    const handlePointerDown = (e: PointerEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    window.addEventListener('pointerdown', handlePointerDown);
    window.addEventListener('keydown', handleKey);
    return () => {
      window.removeEventListener('pointerdown', handlePointerDown);
      window.removeEventListener('keydown', handleKey);
    };
  }, [open]);

  const toggle = () => {
    setOpen((o) => !o);
    setView('root');
    setPendingUserId(null);
    setPinInput('');
    setPinError(null);
  };

  const activateProfile = (id: number) => {
    // BrowserRouter transitions location updates. Change the profile in the
    // same transition so its new guide cannot capture the old channel URL.
    startTransition(() => {
      navigate('/', { replace: true });
      setCurrentUserId(id);
    });
    setOpen(false);
    setView('root');
  };

  const selectUser = (id: number) => {
    const target = users.find((u) => u.id === id);
    if (!target) return;
    if (target.hasPin) {
      setPendingUserId(id);
      setPinInput('');
      setPinError(null);
      setView('pin');
    } else {
      activateProfile(id);
    }
  };

  const submitPin = async () => {
    const target = users.find((u) => u.id === pendingUserId);
    if (!target) return;
    try {
      const result = await verifyPin(target.id, pinInput);
      if (result.valid) {
        activateProfile(target.id);
        return;
      }
      setPinError(result.retryAfterSeconds ? `Too many wrong PINs. Try again in ${result.retryAfterSeconds}s.` : 'Incorrect PIN.');
    } catch {
      setPinError("Couldn't check the PIN. Try again.");
    }
    setPinInput('');
  };

  return (
    <div className="profile-menu" ref={containerRef}>
      <button
        className="page-header-control page-header-control-avatar instant-tooltip"
        onClick={toggle}
        aria-label={`Profile: ${currentUser.name}`}
        data-tooltip={currentUser.name}
      >
        <UserAvatar name={currentUser.name} icon={currentUser.icon} size={28} />
      </button>

      {open && (
        <div className={'profile-menu-panel' + (view !== 'root' ? ' profile-menu-expanded' : '')}>
          {view === 'root' && (
            <>
              <button className="profile-menu-item" onClick={() => go('/dashboard')}>
                Dashboard
              </button>
              <button className="profile-menu-item" onClick={() => go('/profile')}>
                Profile Settings
              </button>
              <button className="profile-menu-item" onClick={() => setView('switch')}>
                Switch Profile
              </button>
              {isAdmin && (
                <button className="profile-menu-item" onClick={() => go('/account')}>
                  Admin Settings
                </button>
              )}
              {isOwner && (
                <button className="profile-menu-item" onClick={() => go('/admin/accounts')}>
                  Server Settings
                </button>
              )}
              <button className="profile-menu-item" onClick={() => void signOut()}>
                Sign Out
              </button>
            </>
          )}

          {view === 'switch' && (
            <>
              <button className="profile-menu-item profile-menu-back" onClick={() => setView('root')}>
                ← Back
              </button>
              {users.map((u) => (
                <button
                  key={u.id}
                  className={'profile-menu-item profile-menu-profile' + (u.id === currentUser.id ? ' profile-menu-item-active' : '')}
                  onClick={() => selectUser(u.id)}
                >
                  <span className="profile-menu-avatar" aria-hidden="true">
                    <UserAvatar name={u.name} icon={u.icon} size={28} />
                  </span>
                  <span className="profile-menu-name">{u.name}</span>
                  <span className="profile-menu-role">
                    {u.hasPin ? '🔒 ' : ''}
                    {roleLabel(u.role)}
                  </span>
                </button>
              ))}
            </>
          )}

          {view === 'pin' && (
            <>
              <button
                className="profile-menu-item profile-menu-back"
                onClick={() => {
                  setView('switch');
                  setPendingUserId(null);
                  setPinError(null);
                }}
              >
                ← Back
              </button>
              <div className="profile-menu-pin-prompt">
                <label>Enter PIN for {users.find((u) => u.id === pendingUserId)?.name}</label>
                <PasswordInput
                  aria-label="Profile PIN"
                  autoComplete="off"
                  inputMode="numeric"
                  maxLength={6}
                  autoFocus
                  value={pinInput}
                  onChange={(e) => setPinInput(e.target.value.replace(/\D/g, ''))}
                  onKeyDown={(e) => e.key === 'Enter' && void submitPin()}
                />
                {pinError && <p className="profile-menu-pin-error">{pinError}</p>}
                <button className="profile-menu-item profile-menu-pin-submit" onClick={() => void submitPin()}>
                  Unlock
                </button>
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}
