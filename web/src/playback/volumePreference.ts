// Volume is local to this browser origin and the server's globally unique profile
// ID (including across accounts). Mute is deliberately never stored with it.
export const VOLUME_KEY_PREFIX = 'alreadyon.volume.';
const memory = new Map<number, number>();
const validVolume = (value: unknown): value is number =>
  typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 1;

export function readVolume(profileId: number): number {
  if (memory.has(profileId)) return memory.get(profileId)!;
  try {
    const raw = localStorage.getItem(VOLUME_KEY_PREFIX + profileId);
    const value: unknown = raw && raw.length < 32 ? JSON.parse(raw) : null;
    if (validVolume(value)) return value;
  } catch {
    // Disabled storage still allows remembering manual changes for this visit.
  }
  return memory.get(profileId) ?? 1;
}

// Call only from an explicit volume adjustment, never a media volumechange event:
// autoplay fallback, mute and stream initialization must not overwrite the choice.
export function writeVolume(profileId: number, volume: number): void {
  if (!validVolume(volume)) return;
  memory.set(profileId, volume);
  try {
    localStorage.setItem(VOLUME_KEY_PREFIX + profileId, JSON.stringify(volume));
  } catch {
    // Playback and in-memory volume still work when browser storage is blocked.
  }
}
