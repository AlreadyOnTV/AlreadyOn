import type { PlaybackSession, SubtitleTrack } from './types';

export interface Cue { s: number; e: number; t: string }

const MAX_CACHED = 8;
const cache = new Map<string, Cue[]>();

const sleep = (ms: number, signal: AbortSignal) => new Promise<void>((resolve, reject) => {
  const timer = setTimeout(resolve, ms);
  signal.addEventListener('abort', () => { clearTimeout(timer); reject(new DOMException('aborted', 'AbortError')); }, { once: true });
});

// The server extracts a track in the background and answers 202 until it is
// ready, which on a large remux can take minutes.
export async function loadCues(session: PlaybackSession, track: SubtitleTrack, signal: AbortSignal): Promise<Cue[]> {
  const key = `${session.mediaFileId}:${track.index}`;
  const hit = cache.get(key);
  if (hit) return hit;
  const url = `${session.mediaUrl}/subtitles/${track.index}?token=${encodeURIComponent(session.token)}`;
  for (let attempt = 0; attempt < 300; attempt++) {
    const res = await fetch(url, { signal, cache: 'no-store', credentials: 'omit' });
    if (res.status === 202) { await sleep(2000, signal); continue; }
    if (!res.ok) throw new Error(`subtitles unavailable (${res.status})`);
    const cues: Cue[] = (await res.json()).cues ?? [];
    if (cache.size >= MAX_CACHED) cache.delete(cache.keys().next().value as string);
    cache.set(key, cues);
    return cues;
  }
  throw new Error('subtitles took too long to prepare');
}

// Cues are sorted by start time; a few earlier ones can still be showing.
export function cueAt(cues: Cue[], t: number): Cue | null {
  let lo = 0, hi = cues.length - 1, found = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (cues[mid].s <= t) { found = mid; lo = mid + 1; } else hi = mid - 1;
  }
  for (let i = found; i >= 0 && i > found - 4; i--) if (t < cues[i].e) return cues[i];
  return null;
}

export const trackKey = (track: SubtitleTrack) => track.language || 'und';

// Profiles store ISO 639-1 codes ("en"); files use 639-2 ("eng", "fre" or "fra").
const LANGUAGES: string[][] = [
  ['en', 'eng'], ['es', 'spa'], ['fr', 'fra', 'fre'], ['de', 'deu', 'ger'], ['it', 'ita'], ['pt', 'por'], ['ja', 'jpn'], ['ko', 'kor'],
  ['zh', 'zho', 'chi'], ['ru', 'rus'], ['nl', 'nld', 'dut'], ['sv', 'swe'], ['da', 'dan'], ['no', 'nor', 'nob'], ['fi', 'fin'], ['pl', 'pol'],
  ['tr', 'tur'], ['ar', 'ara'], ['he', 'heb'], ['hi', 'hin'], ['cs', 'ces', 'cze'], ['el', 'ell', 'gre'], ['hu', 'hun'], ['th', 'tha'], ['uk', 'ukr'],
];
export const SUBTITLE_LANGUAGES = LANGUAGES.map(l => l[0]);
export const DEFAULT_SUBTITLE_LANGUAGE = 'en';
export function canonicalLanguage(code: string): string {
  const c = code.toLowerCase();
  return LANGUAGES.find(l => l.includes(c))?.[0] ?? c;
}

// Prefers a full track over a forced (signs-only) one in the chosen language.
export function pickTrack(tracks: SubtitleTrack[], language: string): SubtitleTrack | undefined {
  const same = tracks.filter(t => canonicalLanguage(trackKey(t)) === canonicalLanguage(language));
  return same.find(t => !t.forced) ?? same[0];
}

export function languageLabel(code: string): string {
  if (code === 'und') return 'Unknown language';
  try { return new Intl.DisplayNames(['en'], { type: 'language' }).of(code) ?? code; } catch { return code; }
}

// Compare actual audio, not merely available tracks. Unknown metadata must not
// trigger captions; missing subtitles simply leave the picture uninterrupted.
export function preferredSubtitleLanguage(audio: string | undefined, preferredAudio: string,
  mode: 'always' | 'other_audio' | 'off', tracks: SubtitleTrack[], subtitleLanguage: string): string | null {
  const canonical = (code: string) => canonicalLanguage(code.trim().replace('_', '-').split('-')[0]);
  const language = canonical(audio ?? '');
  const enabled = mode === 'always' || (mode === 'other_audio' && language !== '' && language !== 'und' && language !== canonical(preferredAudio));
  return enabled && pickTrack(tracks, subtitleLanguage) ? subtitleLanguage : null;
}
