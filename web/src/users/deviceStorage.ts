// Device-local active profile. Capability/network measurements have their own
// explicitly allowed cache module; shared application settings remain server-side.
import { VOLUME_KEY_PREFIX } from '../playback/volumePreference';
import { PLAYBACK_CACHE_KEY } from '../playback/deviceCache';
const ACTIVE_PROFILE_KEY = 'alreadyon.currentUserId';
// 2160 (4K) is offered only while the server has a hardware encoder.
export const QUALITY_HEIGHTS = [2160, 1080, 720, 480, 360] as const;

// The playback quality ceiling per profile lives only in memory: it survives
// channel changes, resizing, moving around the app and switching profiles, but a
// fresh page load starts on Auto (0). Reloads the app itself makes (saving
// settings, restarting the server) carry it across through sessionStorage,
// which is read once and removed.
const QUALITY_CARRY_KEY = 'alreadyon.qualityCarry';
const qualities = new Map<number, number>();

function restoreCarriedQualities(): void {
  try {
    const raw = sessionStorage.getItem(QUALITY_CARRY_KEY);
    sessionStorage.removeItem(QUALITY_CARRY_KEY);
    const parsed: unknown = raw && raw.length < 4096 ? JSON.parse(raw) : null;
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return;
    for (const [id, height] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof height === 'number' && (QUALITY_HEIGHTS as readonly number[]).includes(height)) qualities.set(Number(id), height);
    }
  } catch {
    // storage may be disabled; the choice then simply resets on reload
  }
}
restoreCarriedQualities();

export function readQuality(profileId: number | null): number {
  return profileId == null ? 0 : (qualities.get(profileId) ?? 0);
}

export function writeQuality(profileId: number | null, height: number): void {
  if (profileId == null) return;
  if (height === 0) qualities.delete(profileId);
  else qualities.set(profileId, height);
}

// Reloads the page on the app's own behalf, keeping every profile's quality choice.
export function reloadKeepingQuality(): void {
  try {
    sessionStorage.setItem(QUALITY_CARRY_KEY, JSON.stringify(Object.fromEntries(qualities)));
  } catch {
    // storage may be disabled; the choice then resets
  }
  window.location.reload();
}

export function readActiveProfileId(): number | null {
  try {
    const n = Number(localStorage.getItem(ACTIVE_PROFILE_KEY));
    return Number.isInteger(n) && n > 0 ? n : null;
  } catch {
    return null;
  }
}

export function writeActiveProfileId(id: number): void {
  try {
    localStorage.setItem(ACTIVE_PROFILE_KEY, String(id));
  } catch {
    // storage may be disabled (private browsing); the choice just won't persist
  }
}

// Earlier versions kept profiles, themes and Server-tab choices in the
// browser. The server owns them now and nothing imports them, so drop the
// leftovers.
export function clearLegacyStorage(): void {
  try {
    for (const key of Object.keys(localStorage)) {
      if (key.startsWith('alreadyon.') && key !== ACTIVE_PROFILE_KEY && key !== PLAYBACK_CACHE_KEY && !key.startsWith(VOLUME_KEY_PREFIX)) {
        localStorage.removeItem(key);
      }
    }
  } catch {
    // ignore
  }
}
