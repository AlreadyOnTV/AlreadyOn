import Hls from 'hls.js';
import { apiFetch } from '../api/access';
import { CACHE_TTL, CACHE_VERSION, readSnapshot, writeSnapshot, invalidateSnapshot } from './deviceCache';
import type { Capabilities, Snapshot, VideoCapability, AudioCapability } from './types';

let pending: Promise<Capabilities> | null = null;
let pendingForced = false;
let queued: Promise<Capabilities> | null = null;
// Set once this page load has checked the media origin; later tunes then reuse
// a fresh measurement without asking the server for a probe session each time.
let originChecked = false;

async function detect(): Promise<Capabilities> {
  const element = document.createElement('video');
  const nativeHLS = !!element.canPlayType('application/vnd.apple.mpegurl');
  // hls.js can use ManagedMediaSource on iPhone even when MediaSource is
  // absent. Probe the same constructor that the player will actually use.
  const mediaSource = Hls.getMediaSource();
  const mseHLS = Hls.isSupported();
  const hls = nativeHLS || mseHLS;
  const video: VideoCapability[] = [];
  for (const [profile, prefix] of [['High', '6400'], ['Main', '4d00'], ['Constrained Baseline', '42e0'], ['Baseline', '4200']]) {
    for (const [width, height, fps, level, rate] of [[3840, 2160, 30, 51, 25000000], [1920, 1080, 60, 42, 20000000], [1920, 1080, 30, 40, 20000000], [1280, 720, 30, 31, 20000000]]) {
      const codec = `avc1.${prefix}${level.toString(16).padStart(2, '0')}`;
      const contentType = `video/mp4; codecs="${codec}"`;
      if (!element.canPlayType(contentType)) continue;
      if (mseHLS && !mediaSource?.isTypeSupported(contentType)) continue;
      let supported = true;
      if (navigator.mediaCapabilities) {
        try {
          const result = await navigator.mediaCapabilities.decodingInfo({ type: mseHLS ? 'media-source' : 'file', video: { contentType, width, height, bitrate: rate, framerate: fps } });
          supported = result.supported && result.smooth;
        } catch { supported = width <= 1280 && fps <= 30; }
      } else { supported = width <= 1280 && fps <= 30; }
      if (supported) video.push({ codec: 'h264', profile, level, width, height, fps, bitrate: rate, pixelFormat: 'yuv420p' });
    }
  }
  // Probe file playback separately: MSE/HLS support is not a prerequisite for
  // preserving an original MP4/WebM stream. Unknown capabilities fail closed.
  const hdrDisplay = matchMedia('(dynamic-range: high)').matches;
  const probe = async (configuration: MediaDecodingConfiguration) => {
    if (!navigator.mediaCapabilities) return false;
    const contentType = configuration.video?.contentType ?? configuration.audio?.contentType ?? '';
    if (!element.canPlayType(contentType)) return false;
    try {
      const result = await navigator.mediaCapabilities.decodingInfo(configuration);
      return result.supported && result.smooth;
    } catch { return false; }
  };
  const extraVideo: VideoCapability[] = [];
  for (const [width, height, fps] of [[1920, 1080, 60], [3840, 2160, 60]]) {
    for (const tag of ['hvc1', 'hev1']) {
      for (const range of ['sdr8', 'sdr10', 'pq', 'hlg', 'hdr10plus']) {
        const hdr = !range.startsWith('sdr');
        if (hdr && !hdrDisplay) continue;
        const tenBit = range !== 'sdr8';
        const transferFunction = range === 'hlg' ? 'hlg' : hdr ? 'pq' : 'srgb';
        const hdrMetadataType = range === 'hdr10plus' ? 'smpteSt2094-40' : range === 'pq' ? 'smpteSt2086' : undefined;
        const contentType = `video/mp4; codecs="${tag}.${tenBit ? '2.4' : '1.6'}.L153.B0"`;
        if (await probe({ type: 'file', video: { contentType, width, height, framerate: fps, bitrate: 60000000, colorGamut: hdr ? 'rec2020' : 'srgb', transferFunction, hdrMetadataType } })) {
          extraVideo.push({codec: 'hevc', profile: tenBit ? 'Main 10' : 'Main', level: 153, width, height, fps, bitrate: 60000000, pixelFormat: tenBit ? 'yuv420p10le' : 'yuv420p', container: 'mp4', codecTag: tag, colorTransfer: range === 'hlg' ? 'arib-std-b67' : hdr ? 'smpte2084' : '', hdrMetadata: hdrMetadataType});
        }
      }
    }
    for (const [codec, profile, code, level] of [['vp9', 'Profile 0', 'vp09.00.51.08', 51], ['av1', 'Main', 'av01.0.13M.08', 13]] as const) {
      const contentType = `video/webm; codecs="${code}"`;
      if (await probe({type: 'file', video: {contentType, width, height, framerate: fps, bitrate: 40000000}})) extraVideo.push({codec, profile, level, width, height, fps, bitrate: 40000000, pixelFormat: 'yuv420p', container: 'webm'});
    }
  }
  if (hdrDisplay) {
    for (const profile of [5, 8]) for (const tag of ['dvh1', 'dvhe']) {
      const contentType = `video/mp4; codecs="${tag}.${String(profile).padStart(2, '0')}.09"`;
      if (await probe({type: 'file', video: {contentType, width: 3840, height: 2160, framerate: 60, bitrate: 60000000, colorGamut: 'rec2020', transferFunction: 'pq', hdrMetadataType: 'smpteSt2094-10'}})) extraVideo.push({codec: 'hevc', profile: 'Main 10', level: 153, width: 3840, height: 2160, fps: 60, bitrate: 60000000, pixelFormat: 'yuv420p10le', container: 'mp4', codecTag: tag, colorTransfer: 'smpte2084', dolbyVisionProfile: profile, dolbyVisionLevel: 9});
    }
  }
  video.push(...extraVideo);
  const audio: AudioCapability[] = element.canPlayType('audio/mp4; codecs="mp4a.40.2"') ? [{codec: 'aac', channels: 2, sampleRate: 48000, bitrate: 320000}] : [];
  for (const [codec, code, container, bitrate, sampleRate, profile] of [
    ['aac', 'mp4a.40.2', 'mp4', 1024000, 48000, 'LC'],
    ['ac3', 'ac-3', 'mp4', 640000, 48000, ''],
    ['eac3', 'ec-3', 'mp4', 6144000, 48000, ''],
    ['truehd', 'mlpa', 'mp4', 18000000, 192000, ''],
    ['dts', 'dtsc', 'mp4', 1536000, 48000, 'DTS'],
    ['dts', 'dtsh', 'mp4', 24500000, 192000, 'DTS-HD MA'],
    ['flac', 'flac', 'mp4', 10000000, 192000, ''],
    ['opus', 'opus', 'webm', 1024000, 48000, ''],
  ] as const) {
    // Record the largest configuration actually accepted; never infer support
    // for a codec from its availability on the server.
    for (const channels of [10, 8, 6, 2]) {
      const contentType = `audio/${container}; codecs="${code}"`;
      if (!await probe({type: 'file', audio: {contentType, channels: String(channels), bitrate, samplerate: sampleRate}})) continue;
      const spatial = codec === 'eac3' || codec === 'truehd' ? await probe({type: 'file', audio: {contentType, channels: String(channels), bitrate, samplerate: sampleRate, spatialRendering: true}}) : false;
      const hlsAudio = codec === 'aac' && mseHLS && mediaSource?.isTypeSupported(contentType) && await probe({type: 'media-source', audio: {contentType, channels: String(channels), bitrate, samplerate: sampleRate}});
      audio.push({codec, channels, bitrate, sampleRate, container, profile, spatial, hls: hlsAudio});
      break;
    }
  }
  if (mseHLS) for (const channels of [8, 6]) {
    const contentType = 'audio/mp4; codecs="mp4a.40.2"';
    const bitrate = channels === 8 ? 768000 : 512000;
    if (mediaSource?.isTypeSupported(contentType) && await probe({type: 'media-source', audio: {contentType, channels: String(channels), bitrate, samplerate: 48000}})) {
      audio.push({codec: 'aac', channels, sampleRate: 48000, bitrate, container: 'mp4', profile: 'LC', hls: true});
      break;
    }
  }
  return { video, audio, hls, containers: ['mp4', 'mov', 'webm'], mbps: 0 };
}

// Returns the media origin measured against, or null if it is unknown.
async function measure(snapshot: Snapshot, force: boolean): Promise<string | null> {
  // Issuance is cheap and resolves the actual advertised media origin. A fresh
  // cached result avoids any throughput test even across full page reloads.
  const response = await apiFetch('/api/playback/probe', { method: 'POST' });
  if (!response.ok) return null;
  const session: { token: string; mediaUrl: string } = await response.json();
  const origin = new URL(session.mediaUrl).origin;
  const previous = snapshot.network?.origin === origin ? snapshot.network : undefined;
  let updated = false;
  try {
    if (!force && previous && Date.now() - previous.capturedAt < CACHE_TTL && previous.capturedAt <= Date.now() && previous.mbps > 0 && Number.isFinite(previous.mbps)) return origin;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 12000);
    try {
      let measuredMinimum = Infinity;
      let latencyMs: number | undefined;
      const request = async (bytes: number) => {
        const start = performance.now();
        const res = await fetch(`${session.mediaUrl}/probe?bytes=${bytes}&token=${encodeURIComponent(session.token)}`, { signal: controller.signal, cache: 'no-store', credentials: 'omit' });
        if (!res.ok) throw new Error('Network measurement unavailable');
        if (!res.body) throw new Error('Network response has no body');
        const reader = res.body.getReader(); let received = 0;
        try {
          while (true) { const part = await reader.read(); if (part.done) break; received += part.value.byteLength; }
        } finally {
          // A slow connection may reach the overall time budget mid-download.
          // Retain actual measured throughput instead of reverting to 25 Mbps.
          if (bytes > 1 && received > 0) {
            const mbps = received * 8 / (Math.max(performance.now() - start, 0.1) * 1000);
            measuredMinimum = Math.min(measuredMinimum, mbps);
            // Only a latency measured in this run is recorded, never the assumption.
            snapshot.network = { origin, capturedAt: Date.now(), mbps: Math.min(measuredMinimum, 100000), latencyMs };
            updated = true;
          }
          reader.releaseLock();
        }
        if (received !== bytes) throw new Error('Incomplete network measurement');
        return Math.max(performance.now() - start, 0.1);
      };
      await request(1); // Warm the connection; exclude DNS/TLS startup from RTT.
      latencyMs = [await request(1), await request(1), await request(1)].sort((a, b) => a - b)[1];
      const samples: number[] = [];
      for (let i = 0; i < 3; i++) samples.push((4 * 1024 * 1024 * 8) / (await request(4 * 1024 * 1024) * 1000));
      snapshot.network = { origin, capturedAt: Date.now(), mbps: Math.min(...samples, 100000), latencyMs };
      updated = true;
    } finally { clearTimeout(timer); }
  } catch { /* Keep a recent measurement; otherwise the server assumes 25 Mbps. */ }
  finally { void apiFetch('/api/playback/sessions', { method: 'DELETE', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ channelId: 0, token: session.token }) }).catch(() => {}); }
  // A forced refresh (stall or network change) that received nothing must not
  // keep the old, possibly much faster figure: halve it so the next plan is
  // smaller. Repeated stalls are already bounded by the player.
  if (force && !updated && previous) snapshot.network = { origin, capturedAt: Date.now(), mbps: previous.mbps / 2 };
  return origin;
}

export function clientCapabilities(forceNetwork = false): Promise<Capabilities> {
  // A network change during an ordinary refresh still needs a new measurement;
  // concurrent forced callers share one follow-up run.
  if (pending && forceNetwork && !pendingForced) {
    queued ??= pending.catch(() => undefined).then(() => { queued = null; return clientCapabilities(true); });
    return queued;
  }
  if (pending) return pending;
  pendingForced = forceNetwork;
  pending = (async () => {
    let snapshot = readSnapshot();
    if (!snapshot || Date.now() - snapshot.capturedAt >= CACHE_TTL) snapshot = { version: CACHE_VERSION, browser: navigator.userAgent, capturedAt: Date.now(), capabilities: await detect() };
    const fresh = (n?: Snapshot['network']) => !!n && n.capturedAt <= Date.now() && n.mbps > 0 && Date.now() - n.capturedAt < CACHE_TTL;
    // A failed management request must not discard freshly detected codecs.
    const origin = !forceNetwork && originChecked && fresh(snapshot.network) ? snapshot.network!.origin : await measure(snapshot, forceNetwork).catch(() => null);
    if (origin) originChecked = true;
    // A figure measured against another media origin does not describe this one.
    if (origin && snapshot.network && snapshot.network.origin !== origin) delete snapshot.network;
    writeSnapshot(snapshot);
    const n = snapshot.network;
    return { ...snapshot.capabilities, mbps: n && n.capturedAt <= Date.now() && Number.isFinite(n.mbps) && n.mbps > 0 && Date.now() - n.capturedAt < CACHE_TTL ? n.mbps : 0 };
  })().finally(() => { pending = null; pendingForced = false; });
  return pending;
}

// Explicit user retry after installing codecs must bypass the 24-hour snapshot.
// Drain an in-flight probe first so it cannot overwrite the invalidation.
export async function refreshCodecCapabilities(): Promise<void> {
  await Promise.allSettled([pending, queued]);
  invalidateSnapshot();
  await clientCapabilities().catch(() => {});
}

// Called once after management authentication. Refresh on expiry/resume and
// observable connection changes, not each channel switch.
export function watchCapabilities() {
  let stopped = false;
  const refresh = () => { if (!stopped) void clientCapabilities().catch(() => {}); };
  const networkChanged = () => { if (!stopped) void clientCapabilities(true).catch(() => {}); };
  refresh();
  const interval = setInterval(refresh, CACHE_TTL);
  const resume = () => { const s = readSnapshot(); if (document.visibilityState === 'visible' && (!s || Date.now() - s.capturedAt >= CACHE_TTL || !s.network || Date.now() - s.network.capturedAt >= CACHE_TTL)) refresh(); };
  document.addEventListener('visibilitychange', resume);
  window.addEventListener('online', networkChanged);
  const outputsChanged = () => { invalidateSnapshot(); refresh(); };
  const hdrDisplay = matchMedia('(dynamic-range: high)');
  hdrDisplay.addEventListener('change', outputsChanged);
  navigator.mediaDevices?.addEventListener('devicechange', outputsChanged);
  const connection = (navigator as Navigator & { connection?: EventTarget }).connection;
  connection?.addEventListener('change', networkChanged);
  return () => { stopped = true; hdrDisplay.removeEventListener('change', outputsChanged); navigator.mediaDevices?.removeEventListener('devicechange', outputsChanged); clearInterval(interval); document.removeEventListener('visibilitychange', resume); window.removeEventListener('online', networkChanged); connection?.removeEventListener('change', networkChanged); };
}
