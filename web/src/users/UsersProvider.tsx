// Profiles stay server-owned while the provider serializes local edits and refreshes.
import Brand from '../components/Brand';
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import {
  clearProfilePin,
  createProfile,
  deleteProfile,
  getProfiles,
  patchProfile,
  setProfilePin,
  uploadProfileIcon,
  verifyProfilePin,
  type Profile,
  type ProfileDraft,
  type ProfilePatch,
} from '../api/client';
import { UsersContext } from './UsersContext';
import { clearLegacyStorage, readActiveProfileId, writeActiveProfileId } from './deviceStorage';
import './ProfilesGate.css';

export function UsersProvider({ children }: { children: ReactNode }) {
  const [profiles, setProfiles] = useState<Profile[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [activeId, setActiveId] = useState<number | null>(readActiveProfileId);
  // Saves in flight. A refetch that lands meanwhile would overwrite the
  // on-screen change with older server data, so it is skipped.
  const inflight = useRef(0);
  // Also discard refreshes that finish after a creation they straddled.
  const creationRevision = useRef(0);
  const refreshing = useRef(false);

  const load = useCallback((silent: boolean) => {
    if (refreshing.current || inflight.current > 0) return Promise.resolve();
    refreshing.current = true;
    const revision = creationRevision.current;
    // Only the completed request updates React state; starting a refresh does not.
    return getProfiles().then((list) => {
      if (inflight.current > 0 || revision !== creationRevision.current) return;
      setProfiles(list);
      setLoadError(null);
      clearLegacyStorage();
    }).catch((err: unknown) => {
      if (!silent) setLoadError(err instanceof Error ? err.message : 'failed to load profiles');
    }).finally(() => {
      refreshing.current = false;
    });
  }, []);

  useEffect(() => {
    void load(false);
    const refresh = () => {
      if (document.visibilityState === 'visible') void load(true);
    };
    const timer = window.setInterval(refresh, 5 * 60 * 1000);
    window.addEventListener('focus', refresh);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener('focus', refresh);
      document.removeEventListener('visibilitychange', refresh);
    };
  }, [load]);

  if (profiles === null || profiles.length === 0) {
    return (
      <div className="profiles-gate" role="status">
        <Brand tagline />
        {loadError ? (
          <>
            <p>Couldn't load profiles from the server.</p>
            <p className="profiles-gate-detail">{loadError}</p>
            <button
              type="button"
              onClick={() => {
                setLoadError(null);
                void load(false);
              }}
            >
              Retry
            </button>
          </>
        ) : profiles === null ? (
          <p>Loading…</p>
        ) : (
          <p>The server has no profiles.</p>
        )}
      </div>
    );
  }

  const replace = (saved: Profile) => setProfiles((prev) => prev?.map((p) => (p.id === saved.id ? saved : p)) ?? prev);

  const updateUser = async (id: number, patch: ProfilePatch) => {
    setProfiles((prev) => prev?.map((p) => (p.id === id ? { ...p, ...patch } : p)) ?? prev);
    inflight.current++;
    try {
      const saved = await patchProfile(id, patch);
      inflight.current--;
      // With another save still pending, its result is the newer one.
      if (inflight.current === 0) replace(saved);
    } catch (err) {
      inflight.current--;
      await load(true);
      throw err;
    }
  };

  const addUser = async (draft: ProfileDraft) => {
    // A focus refresh must not replace a just-created profile with a stale list.
    inflight.current++;
    creationRevision.current++;
    try {
      const created = await createProfile(draft);
      setProfiles((prev) => [...(prev ?? []).filter(profile => profile.id !== created.id), created]);
      return created;
    } finally {
      inflight.current--;
      creationRevision.current++;
    }
  };

  const deleteUser = async (id: number) => {
    await deleteProfile(id);
    setProfiles((prev) => prev?.filter((p) => p.id !== id) ?? prev);
  };

  const setPin = async (id: number, pin: string) => {
    await setProfilePin(id, pin);
    setProfiles((prev) => prev?.map((p) => (p.id === id ? { ...p, hasPin: true } : p)) ?? prev);
  };

  const clearPin = async (id: number) => {
    await clearProfilePin(id);
    setProfiles((prev) => prev?.map((p) => (p.id === id ? { ...p, hasPin: false } : p)) ?? prev);
  };

  const uploadIcon = async (id: number, image: Blob) => {
    const saved = await uploadProfileIcon(id, image);
    replace(saved);
    return saved;
  };

  const setCurrentUserId = (id: number) => {
    setActiveId(id);
    writeActiveProfileId(id);
  };

  // If the active profile is gone (deleted here or on another device), fall
  // back to an admin rather than pointing at nothing.
  const currentUser = profiles.find((p) => p.id === activeId) ?? profiles.find((p) => p.role === 'admin') ?? profiles[0];

  return (
    <UsersContext.Provider
      value={{
        users: profiles,
        currentUser,
        currentUserId: currentUser.id,
        isAdmin: currentUser.role === 'admin',
        setCurrentUserId,
        addUser,
        updateUser,
        deleteUser,
        setPin,
        clearPin,
        verifyPin: verifyProfilePin,
        uploadIcon,
      }}
    >
      {children}
    </UsersContext.Provider>
  );
}
