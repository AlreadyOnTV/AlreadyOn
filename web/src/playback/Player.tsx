import { useEffect, useMemo, useRef, useState } from "react";
import Hls from "hls.js";
import { APP_VERSION } from "../version";
import { apiFetch } from "../api/access";
import { clientCapabilities } from "./capabilities";
import { BROWSER_CODEC_MESSAGE, CodecHelp } from "./CodecHelp";
import { responsivePlaylistLoader } from "./playlistLoader";
import { PlayerControls } from "./PlayerControls";
import {
  PlaybackDetails,
  TRANSFER_WINDOW_MS,
  type FragmentStat,
  type Transfer,
} from "./PlaybackDetails";
import {
  cueAt,
  DEFAULT_SUBTITLE_LANGUAGE,
  languageLabel,
  loadCues,
  pickTrack,
  preferredSubtitleLanguage,
  trackKey,
  type Cue,
} from "./subtitles";
import {
  QUALITY_HEIGHTS,
  readQuality,
  writeQuality,
} from "../users/deviceStorage";
import { useUsers } from "../users/UsersContext";
import type { AudioTrack, PlaybackSession } from "./types";
import "./Player.css";

type Authorization = Pick<PlaybackSession, "token" | "channelId">;
// quality is the ceiling this stream was requested with (0 = automatic).
type Prepared = {
  value: PlaybackSession;
  receivedAt: number;
  requestedAt: number;
  quality: number;
  audio: string;
};
type RequestError = Error & {
  status?: number;
  code?: string;
  missingCodecs?: string[];
  retryAt?: string;
  serverTime?: string;
};
// Server-provided presentation delay; this fallback matches older servers.
const LIVE_DELAY = 4;
// A first two-second segment is enough to show a picture without waiting for the next.
const START_BUFFER_HLS = 1.5;
const INITIAL_BUFFER_WAIT_MS = 500;
const INITIAL_HARDWARE_BUFFER_WAIT_MS = 0;
const HLS_ALIGNMENT_HEADROOM = 3;
// The next program is prepared ahead of time, so it needs less in hand.
const START_BUFFER_NEXT = 1.5;
const START_BUFFER_DIRECT = 1;
const MAX_START_WAIT_MS = 12000;
const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
async function release(session: Authorization) {
  await apiFetch("/api/playback/sessions", {
    method: "DELETE",
    keepalive: true,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(session),
  }).catch(() => {});
}

interface PlayerApi {
  sourceTime: () => number | null;
  pause: () => void;
  resume: () => void;
  requality: () => Promise<void>;
}

const CONTROLS_IDLE_MS = 3000;
const TOUCH_CONTROLS_IDLE_MS = 3000;

export function Player({
  channelId,
  large,
  onToggleLarge,
}: {
  channelId: number | null;
  large?: boolean;
  onToggleLarge?: () => void;
}) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const stageRef = useRef<HTMLDivElement>(null);
  const api = useRef<PlayerApi | null>(null);
  const [status, setStatus] = useState("Select a channel to watch");
  const [mode, setMode] = useState("");
  const [error, setError] = useState(false);
  const [codecError, setCodecError] = useState(false);
  const [nativeCodecError, setNativeCodecError] = useState(false);
  const [missingCodecs, setMissingCodecs] = useState<string[]>([]);
  const [showCodecHelp, setShowCodecHelp] = useState(false);
  // Forced conversion covers the failing entry only (until its end), so later
  // programs and ads, or the same channel revisited, try direct play again.
  const [retry, setRetry] = useState({ channelId, count: 0, until: 0 });
  const [generation, setGeneration] = useState(0);
  const releasePending = useRef(Promise.resolve());
  const [paused, setPaused] = useState(false);
  useEffect(() => {
    const video = videoRef.current;
    if (!video || !("wakeLock" in navigator)) return;
    let lock: WakeLockSentinel | null = null;
    let pending = false;
    let active = false;
    let disposed = false;
    const wanted = () =>
      active &&
      !disposed &&
      !video.paused &&
      !video.ended &&
      document.visibilityState === "visible";
    const releaseLock = () => {
      const previous = lock;
      lock = null;
      if (previous) void previous.release().catch(() => {});
    };
    const sync = async () => {
      if (!wanted()) {
        releaseLock();
        return;
      }
      if (lock || pending) return;
      pending = true;
      try {
        const acquired = await navigator.wakeLock.request("screen");
        // A pause, hidden tab, or unmount can happen while permission is pending.
        if (!wanted() || acquired.released) {
          void acquired.release().catch(() => {});
          return;
        }
        lock = acquired;
        acquired.addEventListener(
          "release",
          () => {
            if (lock === acquired) lock = null;
          },
          { once: true },
        );
      } catch {
        // OS policy or browser permissions must never interrupt playback.
      } finally {
        pending = false;
      }
    };
    const playing = () => {
      active = true;
      void sync();
    };
    const stopped = () => {
      active = false;
      releaseLock();
    };
    const visibility = () => {
      void sync();
    };
    video.addEventListener("playing", playing);
    video.addEventListener("pause", stopped);
    video.addEventListener("ended", stopped);
    video.addEventListener("emptied", stopped);
    video.addEventListener("error", stopped);
    document.addEventListener("visibilitychange", visibility);
    return () => {
      disposed = true;
      releaseLock();
      video.removeEventListener("playing", playing);
      video.removeEventListener("pause", stopped);
      video.removeEventListener("ended", stopped);
      video.removeEventListener("emptied", stopped);
      video.removeEventListener("error", stopped);
      document.removeEventListener("visibilitychange", visibility);
    };
  }, []);
  const [clock, setClock] = useState({ time: 0, duration: 0 });
  const [volume, setVolume] = useState(1);
  // Browsers only autoplay muted video, so a fresh page starts muted; entering full screen unmutes.
  const [muted, setMuted] = useState(true);
  const [showDetails, setShowDetails] = useState(false);
  // Pointer, touch and player keyboard input reset the three-second overlay deadline.
  const [idle, setIdle] = useState(false);
  const [controlsDismissed, setControlsDismissed] = useState(false);
  const wakeControls = useRef<() => void>(() => {});
  const revealOnlyTap = useRef(false);
  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    let timer: ReturnType<typeof setTimeout>;
    const wake = () => {
      setControlsDismissed(false);
      setIdle(false);
      clearTimeout(timer);
      timer = setTimeout(() => setIdle(true),
        window.matchMedia("(pointer: coarse)").matches
          ? TOUCH_CONTROLS_IDLE_MS : CONTROLS_IDLE_MS);
    };
    // Remember the hidden state before pointerdown wakes React; the subsequent
    // click on the video should reveal controls without toggling playback.
    const pointerDown = (event: PointerEvent) => {
      revealOnlyTap.current = event.pointerType === "touch" &&
        event.target === videoRef.current && stage.classList.contains("is-idle");
      wake();
    };
    wakeControls.current = wake;
    wake();
    stage.addEventListener("pointermove", wake);
    stage.addEventListener("pointerdown", pointerDown);
    stage.addEventListener("pointerup", wake);
    stage.addEventListener("keydown", wake);
    return () => {
      clearTimeout(timer);
      wakeControls.current = () => {};
      stage.removeEventListener("pointermove", wake);
      stage.removeEventListener("pointerdown", pointerDown);
      stage.removeEventListener("pointerup", wake);
      stage.removeEventListener("keydown", wake);
    };
  }, []);
  // Physical rotation should expand touch playback without relying on a mouse
  // gesture. Native fullscreen can be denied; viewport filling remains available.
  const [landscapePlayer, setLandscapePlayer] = useState(false);
  const hasChannel = channelId !== null;
  useEffect(() => {
    const stage = stageRef.current;
    if (!stage || !hasChannel) {
      setLandscapePlayer(false);
      return;
    }
    const query = window.matchMedia("(pointer: coarse) and (orientation: landscape)");
    let disposed = false;
    let requested = false;
    let ownsFullscreen = false;
    let wantsLandscape = false;
    const orientation = window.screen.orientation;
    const sync = (event?: Event) => {
      // Screen orientation can change before viewport media queries catch up.
      const landscape = orientation && event?.target === orientation
        ? window.matchMedia("(pointer: coarse)").matches && orientation.type.startsWith("landscape")
        : query.matches;
      wantsLandscape = landscape;
      setLandscapePlayer(landscape);
      if (!landscape) {
        if (ownsFullscreen && document.fullscreenElement === stage) {
          ownsFullscreen = false;
          void document.exitFullscreen().catch(() => {});
        }
        return;
      }
      if (document.fullscreenElement || requested || !stage.requestFullscreen) return;
      requested = true;
      // Call directly in the orientation event: browsers may permit user-driven
      // rotation even when the usual transient click activation has expired.
      void stage.requestFullscreen().then(() => {
        ownsFullscreen = true;
        if (disposed || !wantsLandscape) {
          ownsFullscreen = false;
          if (document.fullscreenElement === stage) void document.exitFullscreen().catch(() => {});
        }
      }).catch(() => {
        // Browser/OS policy must not block playback or the landscape layout.
      }).finally(() => { requested = false; });
    };
    query.addEventListener("change", sync);
    orientation?.addEventListener("change", sync);
    sync();
    return () => {
      disposed = true;
      query.removeEventListener("change", sync);
      orientation?.removeEventListener("change", sync);
      if (ownsFullscreen && document.fullscreenElement === stage) {
        void document.exitFullscreen().catch(() => {});
      }
    };
  }, [hasChannel]);
  const hlsRef = useRef<Hls | null>(null);
  const fragmentRef = useRef<FragmentStat | null>(null);
  const transfersRef = useRef<Transfer[]>([]);
  const [session, setSession] = useState<PlaybackSession | null>(null);
  const detailSessionToken = showDetails ? session?.token : undefined;
  const detailChannelId = showDetails ? session?.channelId : undefined;
  const detailMode = showDetails ? session?.plan.mode : undefined;
  useEffect(() => {
    if (!detailSessionToken || !detailChannelId || detailMode === "direct")
      return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    let pending: AbortController | undefined;
    // The normal lease heartbeat is intentionally sparse. While diagnostics are
    // visible, poll faster so the first process CPU sample appears promptly.
    const updateDetails = async () => {
      pending = new AbortController();
      try {
        const response = await apiFetch("/api/playback/status", {
          method: "POST",
          signal: pending.signal,
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            channelId: detailChannelId,
            token: detailSessionToken,
          }),
        });
        if (response.ok) {
          const value = (await response.json()) as PlaybackSession;
          if (!stopped)
            setSession((current) =>
              current?.token === detailSessionToken ? value : current,
            );
        }
      } catch {
        // The main playback loop owns status errors and recovery.
      }
      if (!stopped) timer = setTimeout(updateDetails, 2000);
    };
    timer = setTimeout(updateDetails, 1000);
    return () => {
      stopped = true;
      clearTimeout(timer);
      pending?.abort();
    };
  }, [detailSessionToken, detailChannelId, detailMode]);
  const [channelOverlay, setChannelOverlay] = useState(false);
  const overlayChannelId = session?.channelId === channelId ? channelId : null;
  useEffect(() => {
    // Start the five seconds when the new channel's metadata arrives, so tuning
    // latency does not consume its display time. Heartbeats/program changes on
    // the same channel do not restart it; a new tune cancels the previous timer.
    setChannelOverlay(overlayChannelId !== null);
    if (overlayChannelId === null) return;
    const timer = setTimeout(() => setChannelOverlay(false), CONTROLS_IDLE_MS);
    return () => clearTimeout(timer);
  }, [overlayChannelId]);
  // The viewer's own choice (a language, or false for off) stays across programs and channels,
  // not across reloads; until they choose, the profile's default applies.
  const [subtitleChoice, setSubtitleChoice] = useState<string | false | null>(
    null,
  );
  const [notice, setNotice] = useState("");
  const noticeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined,
  );
  const [cues, setCues] = useState<Cue[]>([]);
  const [caption, setCaption] = useState("");
  const freezeRef = useRef<HTMLCanvasElement>(null);
  // The quality ceiling belongs to the profile on this device and applies to every channel.
  const { currentUserId, currentUser } = useUsers();
  const audioRef = useRef<{
    audioLanguage: string;
    audioTrackIndex?: number;
    audioMediaFileId?: number;
  }>({ audioLanguage: currentUser.audioLanguage ?? "en" });
  const chooseAudio = (track: AudioTrack) => {
    if (!session || track.index === session.plan.audioTrackIndex) return;
    if (currentUser.subtitleMode === "other_audio") setSubtitleChoice(null);
    audioRef.current = {
      audioLanguage: track.language || currentUser.audioLanguage || "en",
      audioTrackIndex: track.index,
      audioMediaFileId: session.mediaFileId,
    };
    void api.current?.requality();
  };
  useEffect(() => {
    setSubtitleChoice(null);
    audioRef.current = { audioLanguage: currentUser.audioLanguage ?? "en" };
    void api.current?.requality();
  }, [currentUserId, currentUser.audioLanguage]);
  const profileLanguage =
    currentUser.subtitleLanguage ?? DEFAULT_SUBTITLE_LANGUAGE;
  const selectedAudio = session?.audioTracks?.find(track => track.index === session.plan.audioTrackIndex);
  const defaultLanguage = preferredSubtitleLanguage(selectedAudio?.language, currentUser.audioLanguage ?? "en",
    currentUser.subtitleMode ?? (currentUser.subtitlesEnabled ? "always" : "off"),
    session?.subtitles ?? [], profileLanguage);
  const wantedLanguage =
    subtitleChoice === false
      ? null
      : (subtitleChoice ??
        defaultLanguage);
  const activeTrack =
    session && wantedLanguage
      ? pickTrack(session.subtitles ?? [], wantedLanguage)
      : undefined;
  const subtitleLanguage = activeTrack ? trackKey(activeTrack) : null;
  const showNotice = (message: string) => {
    setNotice(message);
    clearTimeout(noticeTimer.current);
    noticeTimer.current = setTimeout(() => setNotice(""), 2500);
  };
  const toggleSubtitles = () => {
    if (activeTrack) setSubtitleChoice(false);
    else if (!session?.subtitles?.length)
      showNotice("No subtitles for this title");
    else if (!pickTrack(session.subtitles, profileLanguage))
      showNotice(
        `No ${languageLabel(profileLanguage)} subtitles for this title`,
      );
    else setSubtitleChoice(profileLanguage);
  };
  const stored = useMemo(() => readQuality(currentUserId), [currentUserId]);
  const [chosen, setChosen] = useState<{
    profileId: number;
    value: number;
  } | null>(null);
  const quality = chosen?.profileId === currentUserId ? chosen.value : stored;
  const qualityRef = useRef(quality);
  const appliedQuality = useRef(quality);
  const chooseQuality = (height: number) => {
    writeQuality(currentUserId, height);
    setChosen({ profileId: currentUserId, value: height });
  };

  useEffect(() => {
    qualityRef.current = quality;
    if (appliedQuality.current === quality) return;
    appliedQuality.current = quality;
    void api.current?.requality();
  }, [quality]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    video.volume = volume;
    video.muted = muted;
  }, [volume, muted]);

  useEffect(() => {
    const track = activeTrack;
    if (!session || !track) {
      setCues([]);
      return;
    }
    const controller = new AbortController();
    loadCues(session, track, controller.signal).then(
      (c) => {
        if (!controller.signal.aborted) setCues(c);
      },
      () => {},
    );
    return () => controller.abort();
    // A new token for the same file must not refetch: the cue cache is keyed by file and track.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session?.mediaFileId, activeTrack?.index]);

  useEffect(() => {
    if (cues.length === 0) {
      setCaption("");
      return;
    }
    const timer = setInterval(() => {
      const t = api.current?.sourceTime();
      setCaption(t == null ? "" : (cueAt(cues, t)?.t ?? ""));
    }, 200);
    return () => clearInterval(timer);
  }, [cues]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || !channelId) return;
    const controller = new AbortController();
    const grants = new Map<string, Authorization>();
    let stopped = false;
    let current: Prepared | null = null;
    let next: Promise<Prepared | null> | null = null;
    let hls: Hls | null = null;
    let hlsAnchor: { date: number; start: number } | null = null;
    let heartbeat: ReturnType<typeof setTimeout> | undefined;
    let boundary: ReturnType<typeof setTimeout> | undefined;
    let prefetch: ReturnType<typeof setTimeout> | undefined;
    let recovery: ReturnType<typeof setTimeout> | undefined;
    let waiting: ReturnType<typeof setTimeout> | undefined;
    let startTimer: ReturnType<typeof setInterval> | undefined;
    let bufferTimer: ReturnType<typeof setTimeout> | undefined;
    let moving = false;
    let swapping = false;
    // Only the viewer's own pause counts: a paused player stays paused when new
    // data arrives or the next program starts.
    let userPaused = false;
    const viewerId = Array.from(
      crypto.getRandomValues(new Uint8Array(16)),
      (b) => b.toString(16).padStart(2, "0"),
    ).join("");
    let activityTimer: ReturnType<typeof setTimeout> | undefined;
    let activityRequest: AbortController | undefined;
    let reportingActivity = false;
    let activityChanged = false;
    async function reportActivity() {
      if (stopped) return;
      if (reportingActivity) {
        activityChanged = true;
        return;
      }
      clearTimeout(activityTimer);
      reportingActivity = true;
      activityChanged = false;
      const active = current;
      if (active) {
        const s = active.value;
        const resource = performance.getEntriesByName(s.url).at(-1) as
          | PerformanceResourceTiming
          | undefined;
        const measuredLatency =
          fragmentRef.current?.firstByteMs ??
          (resource && resource.responseStart > 0 && resource.requestStart > 0
            ? resource.responseStart - resource.requestStart
            : null);
        const latencyMs =
          measuredLatency != null &&
          Number.isFinite(measuredLatency) &&
          measuredLatency >= 0 &&
          measuredLatency <= 60000
            ? measuredLatency
            : null;
        activityRequest = new AbortController();
        const timeout = setTimeout(() => activityRequest?.abort(), 4000);
        try {
          await apiFetch("/api/playback/activity", {
            method: "POST",
            signal: activityRequest.signal,
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              viewerId,
              channelId,
              token: s.token,
              profileId: currentUserId,
              latencyMs,
              appVersion: APP_VERSION,
              paused: userPaused,
            }),
          });
        } catch {
          /* Observability must never interrupt playback. */
        } finally {
          clearTimeout(timeout);
        }
      }
      reportingActivity = false;
      if (!stopped)
        activityTimer = setTimeout(
          () => void reportActivity(),
          activityChanged ? 0 : 5000,
        );
    }
    activityTimer = setTimeout(() => void reportActivity(), 1000);
    // Set when the stream moved on or ended while paused; the picture stays until resume, which then tunes fresh.
    let resumeFresh = false;
    let started = false;
    let attachedAt = 0;
    const request = async <T,>(
      path: string,
      body: unknown,
      issuing = false,
    ): Promise<T> => {
      const response = await apiFetch(path, {
        method: "POST",
        signal: issuing ? AbortSignal.timeout(15000) : controller.signal,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw Object.assign(
          new Error(
            data.error || `Playback request failed (${response.status})`,
          ),
          {
            status: response.status,
            code: data.code,
            missingCodecs: Array.isArray(data.missingCodecs)
              ? data.missingCodecs.filter(
                  (codec: unknown) => typeof codec === "string",
                )
              : [],
            retryAt: data.retryAt,
            serverTime: data.serverTime,
          },
        );
      }
      return response.json();
    };
    const drop = async (token: string) => {
      const grant = grants.get(token);
      grants.delete(token);
      if (grant) await release(grant);
    };
    // Re-attaching a stream blanks the picture for a moment; keep the last frame on screen meanwhile.
    const freeze = () => {
      const canvas = freezeRef.current;
      if (!canvas || !video.videoWidth) return;
      canvas.width = video.videoWidth;
      canvas.height = video.videoHeight;
      canvas.getContext("2d")?.drawImage(video, 0, 0);
      canvas.hidden = false;
    };
    const thaw = () => {
      if (freezeRef.current) freezeRef.current.hidden = true;
    };
    const detach = () => {
      clearTimeout(waiting);
      clearTimeout(bufferTimer);
      clearInterval(startTimer);
      hls?.destroy();
      hls = null;
      hlsRef.current = null;
      fragmentRef.current = null;
      transfersRef.current = [];
      hlsAnchor = null;
      video.pause();
      video.removeAttribute("src");
      video.load();
    };
    const unload = () => {
      thaw();
      controller.abort();
      clearTimeout(heartbeat);
      clearTimeout(boundary);
      clearTimeout(prefetch);
      detach();
      releasePending.current = Promise.all([...grants.keys()].map(drop)).then(
        () => {},
      );
    };
    const liveDelay = () =>
      current?.value.presentationDelaySeconds ?? LIVE_DELAY;
    const wallTime = () =>
      current
        ? Date.parse(current.value.serverTime) +
          performance.now() -
          current.receivedAt
        : Date.now();
    const forced = (entryStartsAt?: string) =>
      retry.channelId === channelId &&
      retry.count > 0 &&
      (entryStartsAt
        ? Date.parse(entryStartsAt)
        : wallTime() - liveDelay() * 1000) < retry.until;
    const playingTime = () => {
      if (!current) return null;
      const s = current.value;
      const date = hls?.playingDate?.getTime();
      if (date != null)
        return (
          Date.parse(s.entryStartsAt) +
          (s.streamOffsetSeconds - s.sourceOffsetSeconds) * 1000 +
          date -
          Date.parse(s.hlsOrigin)
        );
      return null;
    };
    // The schedule moment (ms) that the displayed frame belongs to.
    const scheduleTime = () => {
      if (!current) return null;
      const s = current.value;
      const playing = playingTime();
      if (playing != null) return playing;
      return hlsAnchor
        ? hlsAnchor.date + (video.currentTime - hlsAnchor.start) * 1000
        : Date.parse(s.entryStartsAt) +
            (video.currentTime -
              s.sourceOffsetSeconds +
              (s.plan.mode === "direct" ? 0 : s.streamOffsetSeconds)) *
              1000;
    };
    // Position within the source file, which is what subtitles and the total length refer to.
    const sourceTime = () => {
      const at = scheduleTime();
      return current && at != null
        ? (at - Date.parse(current.value.entryStartsAt)) / 1000 +
            current.value.sourceOffsetSeconds
        : null;
    };
    const fail = (reason: RequestError) => {
      if (stopped) return;
      const retryAt = reason.retryAt
        ? Date.parse(reason.retryAt)
        : current
          ? Date.parse(current.value.entryEndsAt)
          : 0;
      const now = reason.serverTime
        ? Date.parse(reason.serverTime)
        : wallTime();
      stopped = true;
      setError(true);
      setCodecError(
        reason.code === "client_codec_unsupported" ||
          reason.code === "native_server_decoder_missing",
      );
      setNativeCodecError(reason.code === "native_server_decoder_missing");
      setMissingCodecs(reason.missingCodecs ?? []);
      setStatus(
        reason.code === "client_codec_unsupported"
          ? BROWSER_CODEC_MESSAGE
          : reason.message,
      );
      unload();
      // A bad entry does not freeze the channel forever. Try its next scheduled
      // boundary once; otherwise wait for an explicit retry (no unbounded loop).
      if (retryAt > now)
        recovery = setTimeout(
          () => setGeneration((n) => n + 1),
          Math.min(retryAt - now + liveDelay() * 1000, 2147483647),
        );
    };
    const desired = () => {
      if (!current) return 0;
      const s = current.value;
      const playing = playingTime();
      if (playing != null)
        return Math.max(
          0,
          video.currentTime +
            (wallTime() - liveDelay() * 1000 - playing) / 1000,
        );
      if (hlsAnchor)
        return Math.max(
          0,
          hlsAnchor.start + (wallTime() - hlsAnchor.date) / 1000 - liveDelay(),
        );
      const source =
        (wallTime() - Date.parse(s.entryStartsAt)) / 1000 +
        s.sourceOffsetSeconds -
        liveDelay();
      return Math.max(
        0,
        s.plan.mode === "direct" ? source : source - s.streamOffsetSeconds,
      );
    };
    const alignmentTarget = () => {
      if (!current || moving) return null;
      const direct = current.value.plan.mode === "direct";
      const ranges = direct ? video.seekable : video.buffered;
      if (!ranges.length) return null;
      const first = direct ? 0 : ranges.length - 1;
      const remaining =
        (Date.parse(current.value.entryEndsAt) -
          (wallTime() - liveDelay() * 1000)) /
        1000;
      const headroom = direct
        ? 0.15
        : Math.max(0.5, Math.min(HLS_ALIGNMENT_HEADROOM, remaining - 0.5));
      return Math.max(
        ranges.start(first),
        Math.min(desired(), ranges.end(ranges.length - 1) - headroom),
      );
    };
    const seek = () => {
      const target = alignmentTarget();
      if (target == null) return;
      const delta = target - video.currentTime;
      // New segments/canplay and status refreshes can put the headroom-clamped
      // target behind a running player. Rewinding then repeats audio and video;
      // only initial positioning may move backward. Recovery/resume still catch up.
      if (delta > 0.75 || (!started && delta < -0.75))
        video.currentTime = Math.max(0, target);
    };
    const bufferedAt = (t: number) => {
      for (let i = 0; i < video.buffered.length; i++)
        if (video.buffered.start(i) <= t + 0.25 && t < video.buffered.end(i))
          return video.buffered.end(i) - t;
      return 0;
    };
    // A browser that refuses sound autoplay still allows muted, so retry muted once.
    const refused = (e: unknown) => {
      if (stopped || (e as Error)?.name === "AbortError") return;
      console.warn("Playback did not start:", e);
      if ((e as Error)?.name === "NotSupportedError") {
        failed(true);
        return;
      }
      if ((e as Error)?.name === "NotAllowedError" && !video.muted) {
        video.muted = true;
        setMuted(true);
        void video.play().catch(refused);
        return;
      }
      setStatus("Press play to watch");
      setPaused(true);
    };
    const startPlayback = () => {
      seek();
      started = true;
      clearInterval(startTimer);
      clearTimeout(bufferTimer);
      void video.play().catch(refused);
    };
    let seekedToStart = false;
    let bufferReadyAt: number | null = null;
    let nextProgram = false;
    let initialBufferWait = false;
    // Waits until a few seconds are in hand past where the stream begins (or a
    // timeout), then aligns with the schedule and plays. A plain file is moved to
    // the schedule first.
    const tryStart = () => {
      if (stopped || moving || started || !current || video.readyState < 1)
        return;
      if (userPaused) {
        if (video.readyState >= 2) {
          clearInterval(startTimer);
          thaw();
          setStatus("");
        }
        return;
      }
      const direct = current.value.plan.mode === "direct";
      if (direct && !seekedToStart) {
        seekedToStart = true;
        seek();
      }
      // A short entry (an ad) cannot hold more than it has left to air.
      const left =
        (Date.parse(current.value.entryEndsAt) -
          (wallTime() - liveDelay() * 1000)) /
        1000;
      const base = direct
        ? START_BUFFER_DIRECT
        : nextProgram
          ? START_BUFFER_NEXT
          : START_BUFFER_HLS;
      const need = Math.max(0.5, Math.min(base, left - 0.5));
      // Check the position we will actually play, not the position before alignment.
      const ahead = bufferedAt(alignmentTarget() ?? video.currentTime);
      const now = performance.now();
      const hardwareEncoded =
        !!current.value.details?.pipeline.encoder &&
        current.value.details.pipeline.encoder !== "Software (CPU)";
      const extraWait =
        !direct && initialBufferWait
          ? hardwareEncoded
            ? INITIAL_HARDWARE_BUFFER_WAIT_MS
            : INITIAL_BUFFER_WAIT_MS
          : 0;
      if (ahead >= need && bufferReadyAt === null) {
        bufferReadyAt = now;
        if (extraWait > 0) bufferTimer = setTimeout(tryStart, extraWait);
      } else if (ahead < need) {
        bufferReadyAt = null;
        clearTimeout(bufferTimer);
      }
      // Keep a short head start after the minimum buffer; hardware transcodes need less.
      if (
        (bufferReadyAt !== null && now - bufferReadyAt >= extraWait) ||
        left < 1 ||
        now - attachedAt > MAX_START_WAIT_MS
      )
        startPlayback();
      else setStatus("Buffering…");
    };
    const ready = () => {
      if (stopped || moving) return;
      if (!started) return tryStart();
      // Later data (after a stall) re-aligns with the schedule, unless the viewer paused.
      if (userPaused) return;
      seek();
      void video.play().catch(refused);
    };
    const playing = () => {
      if (!video.dataset.firstPictureMs && video.dataset.startupStartedAt) {
        video.dataset.firstPictureMs = String(
          performance.now() - Number(video.dataset.startupStartedAt),
        );
      }
      thaw();
      setPaused(false);
      clearTimeout(waiting);
      if (!stopped) {
        setError(false);
        setStatus("");
      }
    };
    const failed = (codecFailure: unknown = false) => {
      if (stopped || moving) return;
      if (userPaused && started) {
        resumeFresh = true;
        return;
      }
      if (!forced(current?.value.entryStartsAt)) {
        stopped = true;
        unload();
        setRetry({
          channelId,
          count: 1,
          until: current
            ? Date.parse(current.value.entryEndsAt)
            : wallTime() + 3600000,
        });
      } else {
        // Network failures/stalls are not evidence of a missing codec. Decode
        // failures can also mean damaged media, so the help stays conditional.
        const unsupported = codecFailure === true || video.error?.code === 4;
        const message =
          video.error?.code === 3
            ? "The stream could not play. The browser could not decode the media; the file may be damaged or its codecs may be unsupported."
            : "The stream could not play. The streaming connection failed; the HTTPS endpoint may be unreachable or blocked by the browser.";
        fail(
          Object.assign(new Error(message), {
            code: unsupported ? "client_codec_unsupported" : undefined,
          }),
        );
      }
    };
    const stalled = () => {
      clearTimeout(waiting);
      waiting = setTimeout(() => {
        if (!stopped && !video.paused) {
          void clientCapabilities(true).then(failed, failed);
        }
      }, 8000);
    };
    const position = () => {
      const at = scheduleTime();
      if (at == null) return;
      video.dataset.scheduleTime = String(at);
      const src = sourceTime();
      const s = current!.value;
      setClock({
        time: Math.max(0, src ?? video.currentTime),
        duration:
          s.durationSeconds > 0
            ? s.durationSeconds
            : (Date.parse(s.entryEndsAt) - Date.parse(s.entryStartsAt)) / 1000,
      });
    };
    const syncPaused = () => {
      if (!moving && !stopped) setPaused(video.paused);
    };
    video.addEventListener("loadedmetadata", ready);
    video.addEventListener("canplay", ready);
    video.addEventListener("playing", playing);
    video.addEventListener("error", failed);
    video.addEventListener("waiting", stalled);
    video.addEventListener("timeupdate", position);
    video.addEventListener("play", syncPaused);
    video.addEventListener("pause", syncPaused);
    video.addEventListener("progress", tryStart);
    api.current = {
      sourceTime,
      pause: () => {
        userPaused = true;
        video.pause();
        setPaused(true);
      },
      requality: () => requality(),
      resume: () => {
        userPaused = false;
        if (resumeFresh) {
          resumeFresh = false;
          clearTimeout(prefetch);
          clearTimeout(boundary);
          const stale = next;
          next = null;
          void stale?.then((p) => (p ? drop(p.value.token) : undefined));
          void advance();
          return;
        }
        if (!started) {
          attachedAt = performance.now();
          clearInterval(startTimer);
          startTimer = setInterval(tryStart, 200);
          tryStart();
          return;
        }
        // A pause never falls behind: playback resumes at the live point, not where it stopped.
        seek();
        void video.play().catch(refused);
      },
    };

    async function prepare(
      entryStartsAt?: string,
      stopOnCapacity = false,
    ): Promise<Prepared | null> {
      const requestedAt = performance.now();
      const capabilities = await clientCapabilities();
      for (let attempt = 0; attempt < (entryStartsAt ? 1 : 20); attempt++) {
        if (stopped) return null;
        const maxHeight = qualityRef.current;
        const audio = { ...audioRef.current };
        const grant = await request<Authorization>(
          "/api/playback/sessions",
          { channelId },
          true,
        );
        // Keep only the two fields accepted by the revocation endpoint.
        const authorization = {
          token: grant.token,
          channelId: grant.channelId,
        };
        if (stopped) {
          await release(authorization);
          return null;
        }
        grants.set(grant.token, authorization);
        try {
          let value = await request<PlaybackSession>(
            `/api/channels/${channelId}/tune`,
            {
              profileId: currentUserId,
              capabilities,
              token: grant.token,
              forceEncode: forced(entryStartsAt),
              entryStartsAt,
              maxHeight,
              ...audio,
            },
          );
          let receivedAt = performance.now();
          const readyStartedAt = receivedAt;
          // A failed GPU step retries on the CPU, so a start can take a little longer.
          const deadline = receivedAt + 30000;
          while (!value.ready) {
            if (performance.now() > deadline)
              throw new Error("The stream took too long to start.");
            const pollDelay =
              performance.now() - readyStartedAt < 1500 ? 100 : 300;
            await sleep(pollDelay);
            if (stopped) return null;
            value = await request<PlaybackSession>(
              "/api/playback/status",
              authorization,
            );
            receivedAt = performance.now();
          }
          return {
            value,
            receivedAt,
            requestedAt,
            quality: maxHeight,
            audio: JSON.stringify(audio),
          };
        } catch (e) {
          await drop(grant.token);
          if (stopped) return null;
          if (
            (stopOnCapacity &&
              (e as RequestError).code === "stream_capacity") ||
            entryStartsAt ||
            ![409, 503].includes((e as RequestError).status ?? 0) ||
            attempt === 19
          )
            throw e;
          await sleep(300);
        }
      }
      return null;
    }
    async function update() {
      const active = current;
      if (stopped || !active) return;
      try {
        const value = await request<PlaybackSession>("/api/playback/status", {
          channelId,
          token: active.value.token,
        });
        if (stopped || current !== active) return;
        current = {
          value,
          receivedAt: performance.now(),
          requestedAt: active.requestedAt,
          quality: active.quality,
          audio: active.audio,
        };
        setSession(value);
        if (!userPaused) seek();
        heartbeat = setTimeout(() => void update(), 10000);
      } catch (e) {
        if (stopped || current !== active) return;
        if (userPaused) resumeFresh = true;
        else fail(e as RequestError);
      }
    }
    function attach(prepared: Prepared, following = false) {
      if (stopped) return;
      initialBufferWait = current === null && !following;
      moving = true;
      detach();
      current = prepared;
      moving = false;
      next = null;
      started = false;
      seekedToStart = false;
      bufferReadyAt = null;
      nextProgram = following;
      attachedAt = performance.now();
      video!.dataset.startupStartedAt = String(
        following ? attachedAt : prepared.requestedAt,
      );
      delete video!.dataset.firstPictureMs;
      const s = prepared.value;
      setSession(s);
      void reportActivity();
      video!.dataset.mediaId = String(s.mediaFileId);
      video!.dataset.kind = s.kind;
      setMode(
        s.plan.mode === "direct"
          ? "Direct Play"
          : s.plan.mode === "remux"
            ? "Direct Stream"
            : "Converting",
      );
      setStatus("Tuning…");
      setError(false);
      if (s.plan.mode !== "direct" && Hls.isSupported()) {
        // A ladder stream starts on its small rung, which is ready sooner; adaptive
        // selection then moves up to the main picture once it has loaded.
        // A prepared entry is already several segments ahead when attached. Load
        // its beginning, not hls.js's live edge, or the first frames skip content
        // before our schedule alignment can run (and we must not rewind afterward).
        hls = new Hls({
          startPosition: following ? 0 : -1,
          maxBufferLength: 12,
          backBufferLength: 6,
          liveSyncDurationCount: 2,
          startLevel: s.plan.lowHeight ? 0 : undefined,
          pLoader: responsivePlaylistLoader(),
        });
        hls.on(Hls.Events.LEVEL_LOADED, (_event, data) => {
          const fragment = data.details.fragments[0];
          if (fragment?.programDateTime != null) {
            const sourceDate =
              Date.parse(s.entryStartsAt) +
              (s.streamOffsetSeconds - s.sourceOffsetSeconds) * 1000;
            hlsAnchor = {
              date:
                sourceDate + fragment.programDateTime - Date.parse(s.hlsOrigin),
              start: fragment.start,
            };
          }
        });
        hlsRef.current = hls;
        hls.on(Hls.Events.FRAG_LOADED, (_event, data) => {
          const { stats, duration } = data.frag;
          fragmentRef.current = {
            loadMs: stats.loading.end - stats.loading.start,
            firstByteMs: stats.loading.first - stats.loading.start,
            bytes: stats.total,
            seconds: duration,
          };
          if (stats.total > 0 && stats.loading.end >= stats.loading.first) {
            const now = performance.now();
            transfersRef.current = [
              ...transfersRef.current.filter(
                (t) => now - t.end <= TRANSFER_WINDOW_MS,
              ),
              {
                bytes: stats.total,
                first: stats.loading.first,
                end: stats.loading.end,
              },
            ];
          }
        });
        hls.on(Hls.Events.ERROR, (_event, data) => {
          if (data.fatal)
            failed(
              data.details ===
                Hls.ErrorDetails.MANIFEST_INCOMPATIBLE_CODECS_ERROR ||
                data.details ===
                  Hls.ErrorDetails.BUFFER_INCOMPATIBLE_CODECS_ERROR,
            );
        });
        hls.loadSource(s.url);
        hls.attachMedia(video!);
      } else {
        video!.src = s.url;
        video!.load();
      }
      startTimer = setInterval(tryStart, 200);
      clearTimeout(heartbeat);
      heartbeat = setTimeout(() => void update(), 10000);
      const remaining = Math.max(
        0,
        Date.parse(s.entryEndsAt) + liveDelay() * 1000 - wallTime(),
      );
      // Prepare one delay before the server-clock boundary and switch one delay
      // after it; the server selects entries at the same presented moment.
      // Entries shorter than the delay prepare their successor immediately.
      prefetch = setTimeout(
        () => {
          next = prepare(s.entryEndsAt).catch(() => null);
        },
        Math.max(0, remaining - 2000 * liveDelay()),
      );
      boundary = setTimeout(() => void advance(), remaining);
    }
    // Switches the airing entry to the newly chosen quality. The new stream is
    // prepared while the old one keeps playing; only then is the picture
    // swapped, holding the last frame for the moment the new stream needs.
    async function requality() {
      if (stopped || moving || swapping || !current) return;
      swapping = true;
      let released = false;
      try {
        let fresh: Prepared | null;
        try {
          fresh = await prepare(undefined, true);
        } catch (e) {
          if (
            (e as RequestError).code !== "stream_capacity" ||
            stopped ||
            moving
          )
            throw e;
          // A replacement cannot fit alongside the current conversion. Release
          // only this player's grants; another viewer may still share the worker.
          freeze();
          moving = true;
          released = true;
          setStatus("Changing playback settings…");
          clearTimeout(heartbeat);
          clearTimeout(prefetch);
          clearTimeout(boundary);
          clearTimeout(waiting);
          const pending = next;
          next = null;
          detach();
          current = null;
          await Promise.all([...grants.keys()].map(drop));
          const prefetched = await pending;
          if (prefetched) await drop(prefetched.value.token);
          // Bounded retries allow the cancelled worker to exit before admission.
          fresh = await prepare();
        }
        if (!fresh) return;
        const old = current;
        if (stopped) return;
        if (
          !released &&
          old &&
          fresh.value.mediaFileId === old.value.mediaFileId &&
          JSON.stringify(fresh.value.plan) === JSON.stringify(old.value.plan)
        ) {
          // The ceiling changes nothing for this title (for example it is below the ceiling already).
          old.quality = fresh.quality;
          old.audio = fresh.audio;
          await drop(fresh.value.token);
        } else {
          clearTimeout(prefetch);
          clearTimeout(boundary);
          const pending = next;
          next = null;
          void pending?.then((p) => (p ? drop(p.value.token) : undefined));
          freeze();
          attach(fresh, true);
          if (old) void drop(old.value.token);
        }
      } catch (e) {
        if (released) {
          if (!stopped)
            fail(
              new Error(
                (e as RequestError).code === "stream_capacity"
                  ? "No conversion slot is available. Try playback again."
                  : "Could not change the playback settings. Try playback again.",
              ),
            );
          return;
        }
        if (!stopped) {
          setStatus("Could not change the playback settings");
          setTimeout(() => {
            if (!stopped && video!.readyState >= 2) setStatus("");
          }, 3000);
        }
        return;
      } finally {
        swapping = false;
      }
      if (
        !stopped &&
        current &&
        (current.quality !== qualityRef.current ||
          current.audio !== JSON.stringify(audioRef.current))
      )
        void requality();
    }
    async function advance() {
      if (stopped || moving) return;
      // The picture stays exactly as it is until the viewer resumes.
      if (userPaused) {
        resumeFresh = true;
        return;
      }
      moving = true;
      setStatus("Tuning…");
      clearTimeout(heartbeat);
      clearTimeout(waiting);
      video!.pause();
      const old = current;
      // Release before a fallback start so a single-process server can progress.
      if (old) await drop(old.value.token);
      try {
        let prepared = next ? await next : null;
        if (
          prepared &&
          (prepared.audio !== JSON.stringify(audioRef.current) ||
            Date.parse(prepared.value.entryEndsAt) <=
              wallTime() - liveDelay() * 1000)
        ) {
          await drop(prepared.value.token);
          prepared = null;
        }
        prepared ??= await prepare();
        if (prepared && !stopped) {
          attach(prepared, true);
          if (
            prepared.quality !== qualityRef.current ||
            prepared.audio !== JSON.stringify(audioRef.current)
          )
            setTimeout(() => void requality(), 0);
        }
      } catch (e) {
        if (!stopped) fail(e as RequestError);
      }
    }
    async function start() {
      setStatus("Tuning…");
      setError(false);
      setCodecError(false);
      setNativeCodecError(false);
      setMissingCodecs([]);
      setShowCodecHelp(false);
      setMode("");
      setSession(null);
      setClock({ time: 0, duration: 0 });
      try {
        await releasePending.current;
        await sleep(150);
        const prepared = await prepare();
        if (prepared && !stopped) {
          attach(prepared);
          if (
            prepared.quality !== qualityRef.current ||
            prepared.audio !== JSON.stringify(audioRef.current)
          )
            setTimeout(() => void requality(), 0);
        }
      } catch (e) {
        if (!stopped) fail(e as RequestError);
      }
    }
    void start();
    const shutdown = () => {
      if (stopped) return;
      stopped = true;
      clearTimeout(activityTimer);
      activityRequest?.abort();
      void apiFetch("/api/playback/activity", {
        method: "DELETE",
        keepalive: true,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ viewerId }),
      }).catch(() => {});
      clearTimeout(recovery);
      api.current = null;
      video.removeEventListener("loadedmetadata", ready);
      video.removeEventListener("canplay", ready);
      video.removeEventListener("playing", playing);
      video.removeEventListener("error", failed);
      video.removeEventListener("waiting", stalled);
      video.removeEventListener("timeupdate", position);
      video.removeEventListener("play", syncPaused);
      video.removeEventListener("pause", syncPaused);
      video.removeEventListener("progress", tryStart);
      unload();
    };
    // Full navigation/reload does not run React unmount cleanup. Release the
    // old worker immediately instead of occupying a slot until its lease expires.
    const restored = (event: PageTransitionEvent) => {
      if (event.persisted) setGeneration((value) => value + 1);
    };
    window.addEventListener("pagehide", shutdown);
    window.addEventListener("pageshow", restored);
    return () => {
      window.removeEventListener("pagehide", shutdown);
      window.removeEventListener("pageshow", restored);
      shutdown();
    };
  }, [channelId, retry, generation, currentUserId]);

  const togglePlay = () => {
    if (paused) api.current?.resume();
    else api.current?.pause();
  };
  const pictureInPicture = () => {
    const video = videoRef.current;
    if (!video) return;
    if (document.pictureInPictureElement) void document.exitPictureInPicture();
    else void video.requestPictureInPicture?.().catch(() => {});
  };
  const fullscreen = () => {
    if (document.fullscreenElement) void document.exitFullscreen();
    else {
      setMuted(false);
      void stageRef.current?.requestFullscreen?.().catch(() => {});
    }
  };
  // Leaving full screen first, so the large player (or small one) is actually shown.
  const toggleLarge =
    onToggleLarge &&
    (() => {
      if (document.fullscreenElement) void document.exitFullscreen();
      onToggleLarge();
    });
  // m, f, t, c, s and Space work in every player size; the guide owns the arrow keys.
  const keys = useRef({
    togglePlay,
    toggleMute: () => setMuted((m) => !m),
    fullscreen,
    toggleLarge: toggleLarge,
    toggleSubtitles,
    toggleDetails: () => setShowDetails((on) => !on),
  });
  useEffect(() => {
    keys.current = {
      togglePlay,
      toggleMute: () => setMuted((m) => !m),
      fullscreen,
      toggleLarge: toggleLarge,
      toggleSubtitles,
      toggleDetails: () => setShowDetails((on) => !on),
    };
  });
  useEffect(() => {
    const ignored = (e: KeyboardEvent) =>
      e.ctrlKey ||
      e.metaKey ||
      e.altKey ||
      (e.target instanceof Element &&
        !!e.target.closest(
          "select, input, textarea, dialog, .playback-message, .profile-menu, .page-header",
        ));
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented || ignored(e)) return;
      const key = e.key.toLowerCase();
      if (key === " ") keys.current.togglePlay();
      else if (key === "m") keys.current.toggleMute();
      else if (key === "f") keys.current.fullscreen();
      else if (key === "t" && keys.current.toggleLarge)
        keys.current.toggleLarge();
      else if (key === "c") keys.current.toggleSubtitles();
      else if (key === "s") keys.current.toggleDetails();
      else return;
      wakeControls.current();
      e.preventDefault();
    };
    // A focused button (a guide slot) would otherwise be pressed when Space is released.
    const onKeyUp = (e: KeyboardEvent) => {
      if (e.key === " " && !ignored(e)) e.preventDefault();
    };
    window.addEventListener("keydown", onKeyDown);
    window.addEventListener("keyup", onKeyUp);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("keyup", onKeyUp);
    };
  }, []);
  return (
    <div
      className={
        "playback-player" +
        (large ? " is-large" : "") +
        (landscapePlayer ? " is-landscape-player" : "") +
        (paused ? " is-paused" : "") +
        (idle ? " is-idle" : "") +
        (controlsDismissed ? " controls-dismissed" : "") +
        (channelOverlay ? " show-channel-overlay" : "")
      }
      data-mode={mode}
      ref={stageRef}
    >
      <video
        ref={videoRef}
        playsInline
        crossOrigin="anonymous"
        aria-label="Channel Playback"
        onClick={() => {
          if (revealOnlyTap.current) {
            revealOnlyTap.current = false;
            return;
          }
          togglePlay();
        }}
      />
      <canvas
        ref={freezeRef}
        className="playback-freeze"
        hidden
        aria-hidden="true"
      />
      {notice && (
        <div className="playback-notice" role="status">
          {notice}
        </div>
      )}
      {showDetails && channelId && session && (
        <PlaybackDetails
          session={session}
          videoRef={videoRef}
          hlsRef={hlsRef}
          transfersRef={transfersRef}
        />
      )}
      {caption && (
        <div className="playback-subtitles" aria-live="off">
          {caption}
        </div>
      )}
      {status && (
        <div className="playback-message" role={error ? "alert" : "status"}>
          {status}
          {error && codecError && (
            <button type="button" onClick={() => setShowCodecHelp(true)}>
              More Info
            </button>
          )}
        </div>
      )}
      {showCodecHelp && (
        <CodecHelp
          nativeServer={nativeCodecError}
          missingCodecs={missingCodecs}
          onClose={() => setShowCodecHelp(false)}
        />
      )}
      {channelId && session && (
        <PlayerControls
          onWakeControls={() => wakeControls.current()}
          onHideControls={() => { setControlsDismissed(true); setIdle(true); }}
          session={session.channelId === channelId ? session : undefined}
          paused={paused}
          time={clock.time}
          duration={clock.duration}
          volume={volume}
          muted={muted}
          quality={quality}
          qualities={QUALITY_HEIGHTS.filter(
            (h) =>
              (h !== 2160 || session.uhd) &&
              (!session.sourceHeight || h <= session.sourceHeight),
          )}
          tracks={session.subtitles ?? []}
          audioTracks={session.audioTracks ?? []}
          audioTrackIndex={session.plan.audioTrackIndex}
          onAudioTrack={chooseAudio}
          subtitleLanguage={subtitleLanguage}
          onTogglePlay={togglePlay}
          onVolume={(v) => {
            setVolume(v);
            setMuted(v === 0);
          }}
          onToggleMute={() => setMuted((m) => !m)}
          onQuality={chooseQuality}
          onSubtitleLanguage={(language) =>
            setSubtitleChoice(language ?? false)
          }
          onPictureInPicture={pictureInPicture}
          detailsOn={showDetails}
          onToggleDetails={() => setShowDetails((on) => !on)}
          onFullscreen={fullscreen}
          large={large}
          onToggleLarge={toggleLarge}
        />
      )}
    </div>
  );
}
