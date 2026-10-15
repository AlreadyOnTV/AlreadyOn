// Minimal API client stub. Expand as the versioned HTTP API (see api/openapi.yaml)
// grows; keep endpoint paths and response types in sync with the server.
import { apiFetch as fetch } from './access';

// The UI is always served by the same server as the API (in local dev, Vite
// proxies /api to it), so requests are same-origin.
const API_BASE = '';

export interface HealthStatus {
  status: string;
}

export async function getHealth(): Promise<HealthStatus> {
  const res = await fetch(`${API_BASE}/healthz`);
  if (!res.ok) {
    throw new Error(`health check failed: ${res.status}`);
  }
  return res.json();
}

// Asks the server to shut down cleanly and start again; it answers first.
export async function restartServer(): Promise<void> {
  await apiRequest('/api/server/restart', jsonInit('POST', {}), 'failed to restart the server');
}

export type LogLevel = 'debug' | 'info' | 'warn' | 'error' | 'critical';

export interface LogEntry {
  time: string;
  level: LogLevel;
  message: string;
}

export interface LogsResponse {
  // The server's IANA time zone; log times are shown in it.
  timeZone: string;
  entries: LogEntry[];
}

export async function getLogs(levels: LogLevel[], limit: number): Promise<LogsResponse> {
  const params = new URLSearchParams();
  if (levels.length > 0) {
    params.set('levels', levels.join(','));
  }
  params.set('limit', String(limit));

  const res = await fetch(`${API_BASE}/api/logs?${params.toString()}`);
  if (!res.ok) {
    throw new Error(`failed to load logs: ${res.status}`);
  }
  return res.json();
}

export type StartupPhase = 'pending' | 'scanning' | 'classifying' | 'populating' | 'scheduling' | 'ready' | 'failed';

export interface StartupStatus {
  phase: StartupPhase;
  active: boolean;
  trigger?: 'startup' | 'refresh' | 'poll';
  startupComplete: boolean;
  // Usable media folders configured on the server; 0 means nothing will ever be scanned.
  mediaRoots: number;
  startedAt: string | null;
  finishedAt: string | null;
  lastError?: string;
  library: {
    filesFound: number;
    filesProbed: number;
    itemsIdentified: number;
    itemsClassified: number;
    channelsWithContent: number;
  };
}

export async function getStartupStatus(): Promise<StartupStatus> {
  const res = await fetch(`${API_BASE}/api/status/startup`);
  if (!res.ok) {
    throw new Error(`failed to load startup status: ${res.status}`);
  }
  return res.json();
}

// Auto shows a channel while it has content, Show always does, Hide never does.
export type ChannelVisibility = 'auto' | 'show' | 'hide';

export interface Channel {
  id: number;
  number: number;
  name: string;
  // The effective result of visibility: what the guide does.
  hidden: boolean;
  visibility: ChannelVisibility;
  // How many distinct shows/movies (not episodes) are in this
  // channel's lineup right now — see server's channel.SeriesCounts.
  itemCount: number;
  // The rule the channel's lineup is built from; null only for a
  // hand-managed or demo channel.
  filter: ChannelFilter | null;
}

export type ChannelDraft = Omit<Channel, 'id' | 'hidden' | 'visibility' | 'itemCount' | 'filter'> & {
  // Absent leaves the filter alone, null removes it, an object saves it.
  filter?: ChannelFilter | null;
};

export type FilterMediaType = 'tv' | 'movie' | 'both';
export type FilterEra = 'modern' | 'nostalgia' | 'both';

// What a channel airs; see server/internal/channel/filter.go.
export interface ChannelFilter {
  excludedGenres?: string[];
  requireKnownYear?: boolean;
  ageRestrictionId?: string;
  eras?: string[];
  titlePhrases?: string[];
  excludedPaths?: string[];
  includedPaths?: string[];
  audienceTier: AudienceTier;
  genres: string[];
  mediaType: FilterMediaType;
  era: FilterEra;
  titleContains: string;
}

export interface Genre {
  id: string;
  label: string;
}

export const DEFAULT_CHANNEL_FILTER: ChannelFilter = {
  audienceTier: 'adult',
  genres: [],
  mediaType: 'both',
  era: 'both',
  titleContains: '',
};

// A scanned file that isn't on any channel, and why.
export interface UnmatchedItem {
  tmdbId?: number;
  id: number;
  path: string;
  mediaType: 'movie' | 'episode';
  title: string;
  seriesTitle: string | null;
  season: number | null;
  episode: number | null;
  year: number | null;
  // null until classified; an empty list means no genres were found.
  genres: string[] | null;
  certification: string | null;
  isManual: boolean;
  reason: string;
}

// Only the fields present change; null clears (or, for genres and
// certification, returns the item to automatic values).
export interface LibraryItemPatch {
  mediaType?: 'movie' | 'episode';
  title?: string;
  seriesTitle?: string | null;
  year?: number | null;
  season?: number | null;
  episode?: number | null;
  genres?: string[] | null;
  certification?: string | null;
}

export function getUnmatched(limit: number, offset: number, signal?: AbortSignal): Promise<{ total: number; items: UnmatchedItem[] }> {
  return apiRequest(`/api/library/unmatched?limit=${limit}&offset=${offset}`, { signal }, 'failed to load unmatched content');
}

export function getCertifications(): Promise<{
  movie: string[];
  tv: string[];
}> {
  return apiRequest('/api/library/certifications', undefined, 'failed to load ratings');
}

export function updateLibraryItem(id: number, patch: LibraryItemPatch): Promise<{ updated: number }> {
  return apiRequest(`/api/library/items/${id}`, jsonInit('PATCH', patch), 'failed to save item');
}

export function updateLibraryItems(ids: number[], patch: LibraryItemPatch): Promise<{ updated: number }> {
  return apiRequest('/api/library/items', jsonInit('PATCH', { ids, ...patch }), 'failed to save items');
}

export function getGenres(): Promise<Genre[]> {
  return apiRequest('/api/library/genres', undefined, 'failed to load genres');
}

export function previewChannelFilter(filter: ChannelFilter): Promise<{ titles: number; items: number }> {
  return apiRequest('/api/channels/filter-preview', jsonInit('POST', filter), 'failed to preview filter');
}

async function parseErrorMessage(res: Response, fallback: string): Promise<string> {
  try {
    const body = await res.json();
    if (body && typeof body.error === 'string') return body.error;
  } catch {
    // ignore — fall through to the generic message
  }
  return `${fallback}: ${res.status}`;
}

export async function getChannels(): Promise<Channel[]> {
  const res = await fetch(`${API_BASE}/api/channels`);
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to load channels'));
  }
  return res.json();
}

export async function createChannel(draft: ChannelDraft): Promise<Channel> {
  const res = await fetch(`${API_BASE}/api/channels`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(draft),
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to create channel'));
  }
  return res.json();
}

export async function updateChannel(id: number, draft: ChannelDraft): Promise<Channel> {
  const res = await fetch(`${API_BASE}/api/channels/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(draft),
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to update channel'));
  }
  return res.json();
}

export async function deleteChannel(id: number): Promise<void> {
  const res = await fetch(`${API_BASE}/api/channels/${id}`, {
    method: 'DELETE',
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to delete channel'));
  }
}

export async function setChannelVisibility(id: number, visibility: ChannelVisibility): Promise<Channel> {
  const res = await fetch(`${API_BASE}/api/channels/${id}/visibility`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ visibility }),
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to update channel visibility'));
  }
  return res.json();
}

export async function bulkSetChannelsVisibility(ids: number[], visibility: ChannelVisibility): Promise<{ updated: number }> {
  const res = await fetch(`${API_BASE}/api/channels/bulk-visibility`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids, visibility }),
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to update channel visibility'));
  }
  return res.json();
}

export interface RefreshScheduleResult {
  classified: boolean;
  enriched: boolean;
  channelsRefreshed: number;
  schedulesRegenerated: number;
}

export async function refreshSchedule(): Promise<RefreshScheduleResult> {
  const res = await fetch(`${API_BASE}/api/admin/refresh-schedule`, {
    method: 'POST',
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to refresh schedule'));
  }
  return res.json();
}

export interface RefreshMediaResult {
  filesAdded: number;
  filesMissing: number;
}

// Rescans the media folders now. profileId is only recorded in the server log.
export interface RefreshTimes {
  mediaRefreshedAt: string | null;
  scheduleRefreshedAt: string | null;
}

// When the library was last scanned and the schedules last regenerated, by any trigger.
export function getRefreshTimes(): Promise<RefreshTimes> {
  return apiRequest('/api/admin/refresh-times', { cache: 'no-store' }, 'failed to load the last refresh times');
}

export async function refreshMedia(profileId: number): Promise<RefreshMediaResult> {
  const res = await fetch(`${API_BASE}/api/admin/refresh-media`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ profileId }),
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to refresh media'));
  }
  return res.json();
}

export type CommercialMode = 'between-only' | 'interval';

export interface CommercialSettings {
  volumeOffsetDb: number;
  enabled: boolean;
  mode: CommercialMode;
  intervalMinutes: number; // 15, 30, or 60 — only meaningful when mode is "interval"
  tvCommercialsPerBreak: number; // 1-5, default 2
  movieCommercialsPerBreak: number; // 1-5, default 5
}

export async function getCommercialSettings(): Promise<CommercialSettings> {
  const res = await fetch(`${API_BASE}/api/settings/commercials`);
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to load commercial settings'));
  }
  return res.json();
}

export async function updateCommercialSettings(settings: CommercialSettings): Promise<CommercialSettings> {
  const res = await fetch(`${API_BASE}/api/settings/commercials`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to save commercial settings'));
  }
  return res.json();
}

export interface MediaSettings {
  pollIntervalMinutes: number; // 15, 30, 60, 120, 240, 480, 720, or 1440
}

export async function getMediaSettings(): Promise<MediaSettings> {
  const res = await fetch(`${API_BASE}/api/settings/media`);
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to load media settings'));
  }
  return res.json();
}

export async function updateMediaSettings(settings: MediaSettings): Promise<MediaSettings> {
  const res = await fetch(`${API_BASE}/api/settings/media`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to save media settings'));
  }
  return res.json();
}

export type FolderPathStatus = 'ok' | 'notAbsolute' | 'notFound' | 'notDirectory' | 'unreadable';
export type FolderSource = 'default' | 'admin';

export interface FolderList {
  paths: { path: string; status: FolderPathStatus; count?: number }[];
  default: string[];
  source: FolderSource;
}

export interface FolderSettings {
  tv: FolderList;
  movies: FolderList;
  commercials: FolderList;
  exclusions: { patterns: string[]; default: string[]; source: FolderSource };
}

// Each field is optional: an array saves that list, null resets it to
// the server's built-in default, and an absent field is left unchanged.
export interface FolderSettingsUpdate {
  tv?: string[] | null;
  movies?: string[] | null;
  commercials?: string[] | null;
  exclusions?: string[] | null;
}

export async function getFolderSettings(): Promise<FolderSettings> {
  const res = await fetch(`${API_BASE}/api/settings/folders`);
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to load folder settings'));
  }
  return res.json();
}

export async function updateFolderSettings(update: FolderSettingsUpdate): Promise<FolderSettings> {
  const res = await fetch(`${API_BASE}/api/settings/folders`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(update),
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to save folder settings'));
  }
  return res.json();
}

export interface GuideProgram {
  mediaFileId: number;
  kind: 'program' | 'commercial';
  title: string;
  seriesTitle?: string;
  seasonNumber?: number;
  episodeNumber?: number;
  mediaType?: string;
  // The movie's own synopsis, or — for an episode — that specific
  // episode's own description (never the parent show's overview).
  // Absent when not yet fetched from TMDb, or nothing came back.
  overview?: string;
  // A movie's release year; the movie's or show's certification; an episode's
  // original air date or a movie's release date (YYYY-MM-DD). Absent when unknown.
  year?: number;
  contentRating?: string;
  airDate?: string;
  startsAt: string; // RFC3339
  endsAt: string; // RFC3339
  // Where within the file playback should start — non-zero only for a
  // program a mid-program commercial break split into multiple segments.
  // Not yet consumed anywhere (no player is wired up yet); present so a
  // future one has it without another API round of changes.
  sourceOffsetSeconds?: number;
}

export interface GuideChannel {
  previousPrograms?: GuideProgram[];
  channelId: number;
  number: number;
  name: string;
  // The audience the channel is set for; absent for a channel without a filter.
  audienceTier?: AudienceTier;
  programs: GuideProgram[];
}

// audienceTier, when given, drops any channel whose lineup contains
// content above that tier's allowed certifications (see the server's
// internal/channel.MinimumAudienceTier) — a household content filter
// asserted by the calling profile, not a security boundary (there's no
// real per-viewer auth yet).
export async function getGuide(hours: number, audienceTier?: string): Promise<GuideChannel[]> {
  // The guide is polled every 30s (see GuidePage) and always needs the
  // current schedule state — cache: "no-store" backs up the server's own
  // Cache-Control: no-store in case a browser or proxy ignores it.
  const tierParam = audienceTier ? `&audienceTier=${encodeURIComponent(audienceTier)}` : '';
  const res = await fetch(`${API_BASE}/api/guide?hours=${hours}${tierParam}`, {
    cache: 'no-store',
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to load guide'));
  }
  return res.json();
}

// MetadataSettingsStatus never carries an actual credential value —
// only whether one is currently configured. The server never sends a
// saved key back to the browser once it's saved (see
// updateMetadataSettings).
export interface MetadataSettingsStatus {
  tmdbApiKeySet: boolean;
  // Opt-in: classify via TVmaze/Wikidata while no TMDb key is set.
  keylessMetadata: boolean;
}

export async function getMetadataSettings(): Promise<MetadataSettingsStatus> {
  const res = await fetch(`${API_BASE}/api/settings/metadata`);
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to load metadata provider settings'));
  }
  return res.json();
}

// MetadataSettingsUpdate: omit a field to leave it untouched (the browser
// never holds the real key to resend); tmdbApiKey "" explicitly clears it,
// a non-empty string replaces it.
export interface MetadataSettingsUpdate {
  tmdbApiKey?: string;
  keylessMetadata?: boolean;
}

export async function updateMetadataSettings(update: MetadataSettingsUpdate): Promise<MetadataSettingsStatus> {
  const res = await fetch(`${API_BASE}/api/settings/metadata`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(update),
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to save metadata provider settings'));
  }
  return res.json();
}

// ApiKey never carries the raw or hashed key value — only what's
// needed to identify and revoke one (see ApiKeyCreated for the one
// exception, at creation time).
export interface ApiKey {
  id: number;
  name: string;
  keyPrefix: string;
  createdAt: string; // RFC3339
}

export interface ApiKeyCreated extends ApiKey {
  key: string; // the full raw key — shown once, here, and never again
}

export async function getApiKeys(): Promise<ApiKey[]> {
  const res = await fetch(`${API_BASE}/api/admin/api-keys`);
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to load API keys'));
  }
  return res.json();
}

export async function createApiKey(name: string): Promise<ApiKeyCreated> {
  const res = await fetch(`${API_BASE}/api/admin/api-keys`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to generate API key'));
  }
  return res.json();
}

export async function revokeApiKey(id: number): Promise<void> {
  const res = await fetch(`${API_BASE}/api/admin/api-keys/${id}`, {
    method: 'DELETE',
  });
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, 'failed to revoke API key'));
  }
}

export type ProfileRole = 'admin' | 'viewer';
export type AudienceTier = 'young_child' | 'teen' | 'adult';

// A profile as the server returns it. Preference fields are null until the
// profile picks one. There is never a PIN here, only whether one is set.
export interface Profile {
  id: number;
  name: string;
  role: ProfileRole;
  audienceTier: AudienceTier;
  hasPin: boolean;
  icon: string | null; // a preset id, or the URL of an uploaded image
  fontLevel: number | null;
  brightnessLevel: number | null;
  guideTimeFormat: string | null;
  subtitleMode: 'always' | 'other_audio' | 'off' | null;
  subtitlesEnabled: boolean | null;
  audioLanguage: string | null;
  subtitleLanguage: string | null; // ISO 639-1, e.g. 'en'
  // The channel picture in the guide's first column; null means shown.
  // Read-only: persisted by a successful tune, shared across devices.
  lastWatchedChannelId: number | null;
}

export interface ProfileDraft {
  name: string;
  role: ProfileRole;
  audienceTier: AudienceTier;
}

// Only the fields present change; null clears a preference or the icon.
export type ProfilePatch = Partial<Omit<Profile, 'id' | 'hasPin' | 'lastWatchedChannelId'>>;

async function apiRequest<T>(path: string, init: RequestInit | undefined, fallback: string): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, init);
  if (!res.ok) {
    throw new Error(await parseErrorMessage(res, fallback));
  }
  return res.status === 204 ? (undefined as T) : res.json();
}

function jsonInit(method: string, body: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  };
}

export function getProfiles(): Promise<Profile[]> {
  return apiRequest('/api/profiles', undefined, 'failed to load profiles');
}

export function getProfile(id: number): Promise<Profile> {
  return apiRequest(`/api/profiles/${id}`, { cache: 'no-store' }, 'failed to load profile');
}

export function createProfile(draft: ProfileDraft): Promise<Profile> {
  return apiRequest('/api/profiles', jsonInit('POST', draft), 'failed to create profile');
}

export function patchProfile(id: number, patch: ProfilePatch): Promise<Profile> {
  return apiRequest(`/api/profiles/${id}`, jsonInit('PATCH', patch), 'failed to save profile');
}

export function deleteProfile(id: number): Promise<void> {
  return apiRequest(`/api/profiles/${id}`, { method: 'DELETE' }, 'failed to delete profile');
}

export function setProfilePin(id: number, pin: string): Promise<void> {
  return apiRequest(`/api/profiles/${id}/pin`, jsonInit('PUT', { pin }), 'failed to set PIN');
}

export function clearProfilePin(id: number): Promise<void> {
  return apiRequest(`/api/profiles/${id}/pin`, { method: 'DELETE' }, 'failed to remove PIN');
}

export function uploadProfileIcon(id: number, file: Blob): Promise<Profile> {
  const form = new FormData();
  form.append('file', file, 'icon.png');
  return apiRequest(`/api/profiles/${id}/icon`, { method: 'POST', body: form }, 'failed to upload icon');
}

export interface PinCheck {
  valid: boolean;
  // Set while the profile is locked after repeated wrong PINs.
  retryAfterSeconds?: number;
}

// A wrong PIN is an expected result, not an error, so 401 and 429 come
// back as data.
export async function verifyProfilePin(id: number, pin: string): Promise<PinCheck> {
  const res = await fetch(`${API_BASE}/api/profiles/${id}/verify-pin`, jsonInit('POST', { pin }));
  if (res.ok || res.status === 401 || res.status === 429) {
    return res.json();
  }
  throw new Error(await parseErrorMessage(res, 'failed to check PIN'));
}

export interface ServerSettings {
  serverName: string;
  transcoding: string;
  tlsMode: string;
  acmeProvider: string;
  acmeDomain: string;
  ddnsProvider: string;
  hardwareDecoding: boolean;
}

// Public TLS metadata never contains either instance or server private keys.
export interface TlsStatus {
 issuedAt: string;
  mode: string;
  names: string[];
  fingerprint: string;
  expiresAt: string;
  trustExpiresAt: string;
  acme: { agreementAccepted: boolean; configured: boolean; hostname: string; email: string; enabled: boolean; running: boolean; lastAttempt: number; lastSuccess: number; nextAttempt: number; message: string; stagingHostname: string };
}
// Explicit staging/production orders require administrator terms acceptance.
export function requestAcmeCertificate(hostname: string, email: string, agree: boolean, staging: boolean): Promise<TlsStatus> {
 return apiRequest('/api/settings/tls/acme', jsonInit('POST', {hostname,email,agree,staging}), 'failed to start certificate issuance');
}
export function getTlsStatus(): Promise<TlsStatus> {
  return apiRequest('/api/settings/tls', undefined, 'failed to load TLS status');
}
export function updateTlsNames(names: string[]): Promise<TlsStatus> {
  return apiRequest('/api/settings/tls', jsonInit('PUT', { names }), 'failed to update certificate names');
}
// Certificate uploads are write-only; responses contain public metadata only.
export function uploadTlsCertificate(certificate: string, privateKey: string, ca: string): Promise<TlsStatus> {
  return apiRequest('/api/settings/tls/upload', jsonInit('POST', { certificate, privateKey, ca }), 'failed to activate certificate');
}
export function restoreSelfSignedTls(): Promise<TlsStatus> {
  return apiRequest('/api/settings/tls/self-signed', jsonInit('POST', {}), 'failed to restore self-signed certificate');
}

export function getServerSettings(): Promise<ServerSettings> {
  return apiRequest('/api/settings/server', undefined, 'failed to load server settings');
}

export function updateServerSettings(update: Partial<ServerSettings>): Promise<ServerSettings> {
  return apiRequest('/api/settings/server', jsonInit('PUT', update), 'failed to save server settings');
}

export interface TranscodingTest {
  encoder: string;
  device?: string;
  ok: boolean;
  detail: string;
}

// Tries a Transcoding choice on the server: a one-second test encode, or with
// decoding set a test decode on that GPU.
export function testTranscoding(transcoding: string, decoding = false): Promise<TranscodingTest> {
  return apiRequest('/api/settings/transcoding/test', jsonInit('POST', { transcoding, decoding }), 'failed to test transcoding');
}

export interface BrowserSession {
	deviceName: string;
	deviceType: string;
	ipAddress: string;
  lastActiveAt: string | null;
  id: number;
  userAgent: string;
  createdAt: string;
  // Null when the session never expires.
  expiresAt: string | null;
  // The session making this request.
  current: boolean;
}

export async function getAuthStatus(): Promise<{ username?: string }> {
  const res = await fetch(`${API_BASE}/api/auth/status`, { cache: 'no-store' });
  if (!res.ok) throw new Error(await parseErrorMessage(res, 'failed to load the account'));
  return res.json();
}

export async function getSessions(): Promise<BrowserSession[]> {
  const res = await fetch(`${API_BASE}/api/auth/sessions`, {
    cache: 'no-store',
  });
  if (!res.ok) throw new Error(await parseErrorMessage(res, 'failed to load sessions'));
  return res.json();
}

// Days a browser stays signed in after it signs in; 0 never expires.
export async function getSessionLifetime(): Promise<number> {
  return (await apiRequest<{ days: number }>('/api/auth/session-lifetime', { cache: 'no-store' }, 'failed to load the sign-in lifetime')).days;
}

export async function setSessionLifetime(days: number): Promise<number> {
  return (await apiRequest<{ days: number }>('/api/auth/session-lifetime', jsonInit('PUT', { days }), 'failed to save the sign-in lifetime')).days;
}

export async function revokeSession(id: number): Promise<void> {
  const res = await fetch(`${API_BASE}/api/auth/sessions/${id}`, {
    method: 'DELETE',
  });
  if (!res.ok) throw new Error(await parseErrorMessage(res, 'failed to sign that session out'));
}

// "others" keeps this session signed in; "all" signs every session out.
export async function revokeSessions(scope: 'others' | 'all'): Promise<number> {
  const res = await fetch(`${API_BASE}/api/auth/sessions/revoke`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ scope }),
  });
  if (!res.ok) throw new Error(await parseErrorMessage(res, 'failed to sign sessions out'));
  return (await res.json()).signedOut;
}

// Other browsers are signed out by the server; this one stays signed in.
export async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  const res = await fetch(`${API_BASE}/api/auth/password`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ currentPassword, newPassword }),
  });
  if (!res.ok) throw new Error(await parseErrorMessage(res, 'failed to change the password'));
}

// Renames the signed-in account; resolves to the saved (trimmed) username.
export async function changeUsername(currentPassword: string, newUsername: string): Promise<string> {
  const res = await fetch(`${API_BASE}/api/auth/username`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ currentPassword, newUsername }),
  });
  if (!res.ok) throw new Error(await parseErrorMessage(res, 'failed to change the username'));
  const data = (await res.json()) as { username: string };
  return data.username;
}

export interface FolderBrowserListing {
  path: string;
  parent: string;
  folders: { name: string; path: string }[];
}

export async function browseFolders(path: string, signal?: AbortSignal): Promise<FolderBrowserListing> {
  const res = await fetch(`${API_BASE}/api/settings/folders/browse?${new URLSearchParams({ path })}`, { signal });
  if (!res.ok) throw new Error(await parseErrorMessage(res, 'Could not open this folder.'));
  return res.json();
}

export interface FilterOption {
  defaultPosition?: number;
  id: string;
  name: string;
  ratings?: string[];
  unrestricted?: boolean;
  allowUnknown?: boolean;
  startYear?: number;
  endYear?: number;
}
export type FilterCatalog = Record<'age' | 'era', FilterOption[]>;
export const getFilterOptions = (): Promise<FilterCatalog> => apiRequest('/api/channel-filter-options', {}, 'Could not load filters');
export const saveFilterOption = (kind: string, option: FilterOption): Promise<FilterOption> =>
  apiRequest(
    '/api/channel-filter-options/' + kind + '/' + encodeURIComponent(option.id),
    { method: 'PUT', body: JSON.stringify(option) },
    'Could not save filter',
  );
export const deleteFilterOption = (kind: string, id: string): Promise<FilterOption> =>
  apiRequest('/api/channel-filter-options/' + kind + '/' + encodeURIComponent(id), { method: 'DELETE' }, 'Could not delete filter');
export const lookupTMDbItem = (id: number, tmdbId: number): Promise<{ updated: number }> =>
  apiRequest('/api/library/items/' + id + '/tmdb', { method: 'POST', body: JSON.stringify({ tmdbId }) }, 'Could not look up TMDb ID');

export const moveFilterOption = (kind: string, id: string, direction: 'up' | 'down' | 'target', target?: string): Promise<void> =>
  apiRequest(
    '/api/channel-filter-options/' + kind + '/' + encodeURIComponent(id) + '/move',
    { method: 'POST', body: JSON.stringify({ direction, target }) },
    'Could not reorder filters',
  );

export interface MetricSample {
  cpuCores?: { core: number; utilization: number | null }[] | null;
  cpuLoad?: number | null;
  cpuPressure?: number | null;
  memoryUsedBytes?: number | null;
  memoryLimitBytes?: number | null;
  memoryLimited?: boolean;
  diskReadMBps?: number | null;
  diskWriteMBps?: number | null;
  ioPressure?: number | null;
  disk?: number | null;
  diskUsedBytes?: number | null;
  diskTotalBytes?: number | null;
  diskPath?: string;
  mediaUsedBytes?: number | null;
  mediaTotalBytes?: number | null;
  diskScope?: string;
  ingressMbps: number | null;
  egressMbps: number | null;
  networkInterface: string;
  networkScope: string;
  at: number;
  cpu: number | null;
  gpu: number | null;
  ram: number | null;
  gpuSource?: string;
}
export interface MetricHistory {
  intervalSeconds: number;
  points: MetricSample[];
  current: MetricSample;
}
export const getServerMetrics = (minutes: number, signal?: AbortSignal): Promise<MetricHistory> =>
  apiRequest('/api/server/metrics?minutes=' + minutes, { signal }, 'Could not load server metrics');

export const restoreFilterOrder = (kind: string): Promise<void> =>
  apiRequest('/api/channel-filter-options/' + kind + '/restore-order', { method: 'POST' }, 'Could not restore default order');

export interface LoudnessStatus {
  enabled: boolean;
  running: boolean;
  currentFile: string;
  active: number;
  workers: number;
  cpuPercent: number;
  limitedBy: string;
  ready: number;
  pending: number;
  skipped: number;
  failed: number;
  lastError?: string;
}
export const getLoudness = (): Promise<LoudnessStatus> => apiRequest('/api/settings/loudness', {}, 'Could not load loudness status');
export const setLoudness = (enabled: boolean): Promise<LoudnessStatus> =>
  apiRequest('/api/settings/loudness', { method: 'PUT', body: JSON.stringify({ enabled }) }, 'Could not save loudness setting');
export const runLoudness = (): Promise<LoudnessStatus> => apiRequest('/api/settings/loudness/run', { method: 'POST' }, 'Could not start loudness analysis');

// Per-channel commercial overrides preserve the global break timing and size.
export interface ChannelCommercialSettings {
  enabled: boolean | null;
  folders: string[];
}
export interface ChannelCommercialPolicy extends ChannelCommercialSettings {
  globalEnabled: boolean;
  effectiveEnabled: boolean;
  availableFolders: string[];
  effectiveFolders: string[];
  foldersFallback: boolean;
}
export async function getChannelCommercials(id: number): Promise<ChannelCommercialPolicy> {
  const response = await fetch(`/api/channels/${id}/commercials`);
  if (!response.ok) throw new Error('Could not load channel commercials');
  return response.json();
}
export async function updateChannelCommercials(id: number, settings: ChannelCommercialSettings): Promise<ChannelCommercialPolicy> {
  const response = await fetch(`/api/channels/${id}/commercials`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  });
  if (!response.ok) {
    const error = await response.json().catch(() => ({}));
    throw new Error(error.error || 'Could not save channel commercials');
  }
  return response.json();
}

// DDNS status never returns provider tokens/passwords. An omitted secret keeps
// the provider's saved credential; clearing it disables automatic updates.
export interface DdnsConfig {
  provider: string; hostname: string; zoneId: string; username: string;
  hasSecret: boolean; enabled: boolean; lastAttempt: number; lastUpdate: number;
  lastVerified: number; lastIp: string; lastError: string; nextAttempt: number; blocked: boolean;
}
export interface DdnsStatus { provider: string; configs: DdnsConfig[]; running: string }
export interface DdnsUpdate {
  provider: string; hostname: string; zoneId?: string; username: string;
  enabled: boolean; secret?: string; clearSecret?: boolean;
}
export function getDdnsStatus(): Promise<DdnsStatus> {
  return apiRequest('/api/settings/ddns', undefined, 'failed to load DDNS settings');
}
export function saveDdns(update: DdnsUpdate): Promise<DdnsStatus> {
  return apiRequest('/api/settings/ddns', jsonInit('PUT', update), 'failed to save DDNS settings');
}
export function testDdns(provider: string): Promise<DdnsStatus> {
  return apiRequest('/api/settings/ddns/test', jsonInit('POST', { provider }), 'failed to update/test DDNS');
}

export function saveAcmeCertificate(hostname:string,email:string,agree:boolean):Promise<TlsStatus> { return apiRequest('/api/settings/tls/acme',jsonInit('PUT',{hostname,email,agree}),'failed to save certificate settings'); }

// Server accounts (owner-only). Each account signs in separately and has its
// own household profiles; maxStreams 0 means no per-account limit.
export type AccountRoleName = 'owner' | 'viewer';

export interface ServerAccount {
  id: number;
  username: string;
  role: AccountRoleName;
  disabled: boolean;
  mustChangePassword: boolean;
  passwordLocked: boolean;
  maxStreams: number;
  createdAtUnix: number;
  updatedAtUnix: number;
}

export interface NewServerAccount {
  username: string;
  password: string;
  role: AccountRoleName;
  mustChangePassword: boolean;
  passwordLocked: boolean;
  maxStreams: number;
}

export type ServerAccountPatch = Partial<Pick<ServerAccount, 'username' | 'role' | 'disabled' | 'mustChangePassword' | 'passwordLocked' | 'maxStreams'>>;

export async function getAccounts(): Promise<ServerAccount[]> {
  return apiRequest<ServerAccount[]>('/api/users', { cache: 'no-store' }, 'failed to load accounts');
}

export async function createAccount(account: NewServerAccount): Promise<ServerAccount> {
  return apiRequest<ServerAccount>('/api/users', jsonInit('POST', account), 'failed to create the account');
}

export async function updateAccount(id: number, patch: ServerAccountPatch): Promise<ServerAccount> {
  return apiRequest<ServerAccount>(`/api/users/${id}`, jsonInit('PATCH', patch), 'failed to update the account');
}

export async function deleteAccount(id: number): Promise<void> {
  return apiRequest<void>(`/api/users/${id}`, { method: 'DELETE' }, 'failed to delete the account');
}

// Sets a new password and signs the account out of every browser and app.
export async function resetAccountPassword(id: number, password: string, mustChangePassword: boolean): Promise<void> {
  return apiRequest<void>(`/api/users/${id}/password`, jsonInit('PUT', { password, mustChangePassword }), 'failed to reset the password');
}

export interface InactiveSessionPolicy { enabled: boolean; days: number }
export async function getInactiveSessionPolicy(): Promise<InactiveSessionPolicy> {
  return apiRequest('/api/auth/session-inactivity', { cache: 'no-store' }, 'failed to load inactivity policy');
}
export async function setInactiveSessionPolicy(policy: InactiveSessionPolicy): Promise<InactiveSessionPolicy> {
  return apiRequest('/api/auth/session-inactivity', jsonInit('PUT', policy), 'failed to save inactivity policy');
}
