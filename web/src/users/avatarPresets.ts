// 10 "auto-generated" preset avatars (emoji + a distinct background
// color) — a mocked stand-in for a real generated-avatar service.
// A user's `icon` is either one of these ids, the URL of an uploaded image
// stored on the server, or unset (falls back to their initial letter).
export interface PresetIcon {
  id: string;
  emoji: string;
  bg: string;
  label: string;
}

export const PRESET_ICONS: PresetIcon[] = [
  { id: 'fox', emoji: '🦊', bg: '#e07a3e', label: 'Fox' },
  { id: 'cat', emoji: '🐱', bg: '#e07ab0', label: 'Cat' },
  { id: 'dog', emoji: '🐶', bg: '#d9a441', label: 'Dog' },
  { id: 'lion', emoji: '🦁', bg: '#e0b93e', label: 'Lion' },
  { id: 'panda', emoji: '🐼', bg: '#7a8a99', label: 'Panda' },
  { id: 'koala', emoji: '🐨', bg: '#8f8f8f', label: 'Koala' },
  { id: 'owl', emoji: '🦉', bg: '#8a6a4a', label: 'Owl' },
  { id: 'frog', emoji: '🐸', bg: '#5fa35f', label: 'Frog' },
  { id: 'monkey', emoji: '🐵', bg: '#b08a5a', label: 'Monkey' },
  { id: 'rabbit', emoji: '🐰', bg: '#a08ad0', label: 'Rabbit' },
];

export function isCustomIcon(icon?: string | null): boolean {
  // "blob:" is a local preview of an image that has not been uploaded yet.
  return !!icon && (icon.startsWith('/') || icon.startsWith('blob:'));
}

export function findPresetIcon(icon?: string | null): PresetIcon | undefined {
  return PRESET_ICONS.find((p) => p.id === icon);
}
