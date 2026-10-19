import Brand from '../components/Brand';
import { circularChannelIndex } from '../playback/channelNavigation';
// Full-screen home page: a 1990s/2000s US cable TV guide, backed by the
// real /api/guide endpoint (see server/internal/guide, #25) — channels
// with no configured/classified content yet just render with an empty
// row (see internal/lineup for how default channels get populated).
//
// Section naming (used in comments/class names below):
//   - page header:     the very top bar (brand + display controls + Profile)
//   - preview section: the bar containing the now-playing detail + video
//   - guide section:   the scrollable channel/time grid ("the guide")

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { mediaTitle } from './mediaTitle';
import { getGuide, getProfile, type GuideChannel, type GuideProgram } from '../api/client';
import { useGuideSettings, type TimeFormat } from '../settings/GuideSettingsContext';
import { formatClock, formatRange } from '../settings/formatTime';
import { useUsers } from '../users/UsersContext';
import { isBusy, useStartupStatus } from '../status/useStartupStatus';
import StartupNotice from '../status/StartupNotice';
import ProfileMenu from './ProfileMenu';
import { Player } from '../playback/Player';
import './GuidePage.css';

// Keep the A1 clock independent of guide fetching and aligned to minute boundaries.
function GuideClock({ format }: { format: TimeFormat }) {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    let timer: number;
    const refresh = () => {
      const current = new Date();
      setNow(current);
      window.clearTimeout(timer);
      timer = window.setTimeout(refresh, 60_000 - (current.getTime() % 60_000));
    };
    refresh();
    window.addEventListener('focus', refresh);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener('focus', refresh);
      document.removeEventListener('visibilitychange', refresh);
    };
  }, []);
  const label = formatClock(now.getHours() * 60 + now.getMinutes(), format);
  return <time dateTime={now.toISOString()} aria-label={`Current time: ${label}`} title="Current time">{label}</time>;
}

// The guide always shows this many hours ahead.
const GUIDE_HOURS = 12;
const DEFAULT_PREVIEW_WIDTH = 360;
const SLOT_MINUTES = 30;
const GUIDE_POLL_INTERVAL_MS = 30_000;
// Phones and tablets keep the simplified large-player experience in either
// orientation. The width fallback also covers narrow desktop windows where the
// compact preview cannot lay out cleanly.
const MOBILE_PLAYER_QUERY = '(hover: none) and (pointer: coarse), (max-width: 700px)';
// One "chunk" is twice the width of a box's start - stop time line and stands for
// this many minutes of runtime; a 30-minute column is scaled from it, so every box
// is as wide as its runtime and a shorter show is narrower (its text is cut off).
const CHUNK_MINUTES = 20;
// Before the fonts are measured.
const FALLBACK_SLOT_WIDTH = 150;

// Exact geometry constants from GuidePage.css's .guide-slot-box, used to
// translate a *measured* text height (see the hidden metrics spans
// below) into the box size that actually contains
// it without cropping. Kept in one place so a CSS tweak there is the
// only thing that ever needs updating here too.
const GUIDE_BOX_BORDER = 1; // border: 1px solid ... (each side)
const GUIDE_BOX_PADDING_V = 6; // padding: 6px 10px (each side, vertical)
const GUIDE_BOX_ROW_GAP = 2; // gap: 2px, between each of the 3 stacked lines
const GUIDE_BOX_VERTICAL_INSET = 3; // top/bottom: 3px (each side, within the row)
// The API's `title`/`seriesTitle` split is the opposite of what you'd
// naively guess: for an episode, `title` is that one episode's own
// title and `seriesTitle` is the show — so the bold heading shows the
// *series*, without the "(2023)" the library adds to tell shows apart, and
// the episode's own title goes in the S01E02 line below (see episodeLabel).
function displayTitle(program: GuideProgram): string {
  return mediaTitle(program.mediaType === 'movie' ? program.title : program.seriesTitle || program.title);
}

// mergeIntervalSegments collapses consecutive schedule entries for the
// *same* underlying media file back into one guide box spanning its
// whole real airtime — commercial.ModeInterval splits a single movie's
// or episode's playback into several segments around mid-program
// breaks (see SourceOffsetSeconds), which otherwise show up in the
// guide as several short, separately-boxed "Movie" listings in a row.
// A real cable guide never listed the same movie twice just because an
// ad broke it up, so this un-fragments it back into one entry the same way.
//
// Two adjacent (post commercial-filtering) entries are one playthrough
// continuing, not a distinct later showing of the same file, when the
// second's SourceOffsetSeconds is strictly greater than the first's —
// a fresh showing always restarts a file at offset 0 (see
// commercial.Settings/schedule.Regenerate), so only a genuine
// continuation ever has it increase. Distinct programs/movies (any
// mediaFileId mismatch) never merge, matching the existing "episodes
// never merge across shows" rule this extends, not replaces.
function mergeIntervalSegments(programs: GuideProgram[]): GuideProgram[] {
  const merged: GuideProgram[] = [];
  for (const program of programs) {
    const prev = merged[merged.length - 1];
    if (prev && prev.mediaFileId === program.mediaFileId && (program.sourceOffsetSeconds ?? 0) > (prev.sourceOffsetSeconds ?? 0)) {
      merged[merged.length - 1] = { ...prev, endsAt: program.endsAt };
    } else {
      merged.push(program);
    }
  }
  return merged;
}

// "Rating: PG-13"; empty when the rating is unknown.
function ratingLabel(program: GuideProgram): string {
  return program.contentRating ? `Rating: ${program.contentRating}` : '';
}

// The second line: an episode's "S01E02 — Its Title", or a movie's content rating.
function episodeLabel(program: GuideProgram): string {
  if (program.mediaType === 'episode' && program.seasonNumber) {
    const s = String(program.seasonNumber).padStart(2, '0');
    const e = String(program.episodeNumber ?? 0).padStart(2, '0');
    const title = mediaTitle(program.title);
    return `S${s}E${e}` + (title ? ` — ${title}` : '');
  }
  if (program.mediaType === 'movie') return ratingLabel(program);
  return '';
}

// ISO date components avoid browser locale and timezone shifts.
function formatAirDate(date: string | undefined): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(date ?? '');
  if (!m) return '';
  return `Air Date: ${m[2]}/${m[3]}/${m[1]}`;
}

// formatClock/formatRange take minutes-since-midnight in the viewer's
// local time — every date the guide ever displays is derived from one
// of these two helpers so that conversion happens in exactly one place.
function clockMinutesOf(iso: string): number {
  const d = new Date(iso);
  return d.getHours() * 60 + d.getMinutes();
}

// currentProgramIndex finds whichever of a channel's own programs is
// actually airing at nowMs — the one thing "the live show" ever means,
// regardless of which index the viewer's cursor happens to be sitting
// on while browsing. programs[0] is normally already the live show
// (the API's own query starts "now"), but this doesn't assume that:
// falls back to 0 only if nothing in the array actually brackets nowMs
// (a data gap right at the edge of what's been generated).
function currentProgramIndex(programs: GuideProgram[], nowMs: number): number {
  const idx = programs.findIndex((p) => {
    const start = new Date(p.startsAt).getTime();
    const end = new Date(p.endsAt).getTime();
    return start <= nowMs && nowMs < end;
  });
  return idx >= 0 ? idx : 0;
}

// currentProgramStartsAt is currentProgramIndex, but returning the
// stable identity Selected actually stores (see its own comment) —
// every call site wants "which program is live," never the index
// itself, so this is what they all actually call.
function currentProgramStartsAt(programs: GuideProgram[], nowMs: number): string {
  return programs[currentProgramIndex(programs, nowMs)].startsAt;
}

// RowMetrics captures what depends only on the viewer's own font size
// (never on any one program's content) — the resolved
// `font` shorthand for each of the 3 stacked lines (for measureTextWidth
// below) and each line's real rendered height (for sizing a row tall
// enough to never crop any of them). Captured once, from real hidden
// DOM elements sharing the exact same classes as a visible box's lines
// (see GuidePage's metrics spans) — that guarantees this matches actual
// rendering exactly with the current typography, without having
// to duplicate that CSS's logic here.
interface RowMetrics {
  titleFont: string;
  episodeFont: string;
  timeFont: string;
  titleHeight: number;
  episodeHeight: number;
  timeHeight: number;
}

// Canvas text measurement: no layout reflow, so cheap enough to call every render.
let measureCtx: CanvasRenderingContext2D | null | undefined;
function measureTextWidth(font: string, text: string): number {
  if (measureCtx === undefined) measureCtx = document.createElement('canvas').getContext('2d');
  if (!measureCtx) return text.length * 7;
  measureCtx.font = font;
  return measureCtx.measureText(text).width;
}

// The widest realistic start - stop label, so one scale fits every box.
function chunkReferenceText(timeFormat: TimeFormat): string {
  return timeFormat === '24h' ? '00:00 - 00:00' : '00:00am - 00:00am';
}

// Canvas and DOM text layout can differ by a hair; keep that from clipping the label.
const TEXT_MEASURE_BUFFER = 2;

function requiredRowMinHeight(metrics: RowMetrics | null): number {
  if (!metrics) return 68; // pre-metrics fallback, matches the old static floor
  const contentHeight = metrics.titleHeight + metrics.episodeHeight + metrics.timeHeight + 2 * GUIDE_BOX_ROW_GAP;
  const boxHeight = contentHeight + 2 * GUIDE_BOX_PADDING_V + 2 * GUIDE_BOX_BORDER;
  return Math.ceil(boxHeight + 2 * GUIDE_BOX_VERTICAL_INSET);
}

// programStartsAt (not a programs[] array index) identifies which
// program is focused/tuned — the array it indexes into isn't stable
// across a re-fetch: mergeIntervalSegments can combine or split
// differently as more of a still-airing segment's future gets fetched,
// so the same index can silently point at a different program a poll
// later than the one the viewer actually had selected. A program's own
// start time is unique within its channel (programs never overlap) and
// untouched by any of that, so it survives a poll landing mid-navigation.
interface Selected {
  channelNumber: number;
  programStartsAt: string;
}

// After a re-fetch the selected program can be gone (it aired, or its segments
// merged differently). Keep the viewer on the same channel, on the program that
// now covers the same moment (else the one airing now), so the preview never
// empties; a channel that has disappeared falls back to the first with content.
function reconcileSelection(sel: Selected | null, channels: GuideChannel[], nowMs: number): Selected | null {
  if (!sel) return sel;
  const channel = channels.find((c) => c.number === sel.channelNumber && c.programs.length > 0);
  if (!channel) {
    const first = channels.find((c) => c.programs.length > 0);
    return first ? { channelNumber: first.number, programStartsAt: currentProgramStartsAt(first.programs, nowMs) } : sel;
  }
  if (channel.programs.some((p) => p.startsAt === sel.programStartsAt)) return sel;
  const at = Date.parse(sel.programStartsAt);
  const covering = channel.programs.find((p) => Date.parse(p.startsAt) <= at && at < Date.parse(p.endsAt));
  return { channelNumber: channel.number, programStartsAt: covering?.startsAt ?? currentProgramStartsAt(channel.programs, nowMs) };
}

export default function GuidePage() {
  const { timeFormat } = useGuideSettings();
  const { currentUser } = useUsers();
  const audienceTier = currentUser.audienceTier;
  // The channel picture in the first column is the profile's choice (shown unless turned off).
  const { status: startupStatus, completions: startupCompletions } = useStartupStatus();
  const [channels, setChannels] = useState<GuideChannel[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [nowMs, setNowMs] = useState(Date.now);
  const gridOrigin = new Date(nowMs);
  gridOrigin.setMinutes(gridOrigin.getMinutes() - (gridOrigin.getMinutes() % SLOT_MINUTES), 0, 0);
  const gridStartMs = gridOrigin.getTime();

  const [focused, setFocused] = useState<Selected | null>(null);
  const [tunedSelection, setTuned] = useState<Selected | null>(null);
  const tuned = useMemo(() => {
    const watchedChannel = channels?.find((c) => c.number === tunedSelection?.channelNumber);
    return watchedChannel?.programs.length
      ? { channelNumber: watchedChannel.number, programStartsAt: currentProgramStartsAt(watchedChannel.programs, nowMs) }
      : tunedSelection;
  }, [channels, tunedSelection, nowMs]);
  const restoreGuideRef = useRef(true);
  const [guideEntry, setGuideEntry] = useState(0);
  useEffect(() => {
    let timer: number;
    const refresh = () => {
      const stamp = Date.now();
      setNowMs(stamp);
      const nextEnd = channels?.flatMap(c => c.programs)
        .map(p => Date.parse(p.endsAt)).filter(end => end > stamp)
        .reduce((next, end) => Math.min(next, end), Infinity) ?? Infinity;
      window.clearTimeout(timer);
      timer = window.setTimeout(refresh, Math.max(1, Math.min(
        30_000, SLOT_MINUTES * 60_000 - stamp % (SLOT_MINUTES * 60_000), nextEnd - stamp,
      )));
    };
    refresh();
    window.addEventListener('focus', refresh);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener('focus', refresh);
      document.removeEventListener('visibilitychange', refresh);
    };
  }, [channels]);
  // The address carries the tuned channel (?channel=124) so a refresh or bookmark returns to it.
  const [searchParams, setSearchParams] = useSearchParams();
  // Capture an explicit link once; discard it after selection or fallback.
  const urlWantedRef = useRef<number | null>(Number(searchParams.get('channel')) || null);
  const tunedChannelNumber = tuned?.channelNumber;
  useEffect(() => {
    document.title = tunedChannelNumber === undefined ? 'AlreadyOn' : `AlreadyOn - CH ${tunedChannelNumber}`;
  }, [tunedChannelNumber]);
  useEffect(() => {
    if (tunedChannelNumber === undefined) return;
    if (urlWantedRef.current === tunedChannelNumber) urlWantedRef.current = null;
    if (urlWantedRef.current !== null) return;
    if (searchParams.get('channel') === String(tunedChannelNumber)) return;
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set('channel', String(tunedChannelNumber));
        return next;
      },
      { replace: true },
    );
  }, [tunedChannelNumber, searchParams, setSearchParams]);
  // Tracks which audienceTier the initial-focus pick below was last run
  // for — re-run it whenever that changes (switching profiles), not
  // just once ever, so switching to a more-restricted profile lands on
  // a channel that's actually still visible to it instead of leaving
  // focus pointed at one that just got filtered out.
  const initialFocusTierRef = useRef<string | null>(null);
  // The large player fills the window's width and hides the show details.
  const [largePlayer, setLargePlayer] = useState(false);
  const [mobilePlayer, setMobilePlayer] = useState(() => window.matchMedia(MOBILE_PLAYER_QUERY).matches);
  const playerLarge = largePlayer || mobilePlayer;
  useEffect(() => {
    const query = window.matchMedia(MOBILE_PLAYER_QUERY);
    const sync = () => setMobilePlayer(query.matches);
    query.addEventListener('change', sync);
    sync();
    return () => query.removeEventListener('change', sync);
  }, []);
  const [fullscreen, setFullscreen] = useState(false);
  useEffect(() => {
    const sync = () => {
      const active = document.fullscreenElement !== null;
      setFullscreen(active);
      if (!active) {
        restoreGuideRef.current = true;
        setNowMs(Date.now());
        setGuideEntry(value => value + 1);
      }
    };
    document.addEventListener('fullscreenchange', sync);
    return () => document.removeEventListener('fullscreenchange', sync);
  }, []);

  // The large player is sized to the space below the header, so publish its height.
  const pageRef = useRef<HTMLDivElement>(null);
  const pageHeaderRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const header = pageHeaderRef.current;
    if (!header) return;
    const publish = () => pageRef.current?.style.setProperty('--page-header-h', `${header.getBoundingClientRect().height}px`);
    publish();
    const observer = new ResizeObserver(publish);
    observer.observe(header);
    return () => observer.disconnect();
  }, []);
  const tunedNow = useRef<Selected | null>(null);
  useEffect(() => {
    tunedNow.current = tuned;
  }, [tuned]);

  const scrollRef = useRef<HTMLDivElement>(null);
  const headRef = useRef<HTMLDivElement>(null);
  const slotRefs = useRef(new Map<string, HTMLButtonElement>());

  // "Time Span Shown" is a hard limit on how far *forward* the guide
  // goes, not just a default zoom level: picking 6 hours shows 6 hours
  // ahead, full stop — nothing further out is reachable by scrolling or
  // arrow-keying past the last column.
  const slotCount = GUIDE_HOURS * 2;

  useEffect(() => {
    let cancelled = false;
    // Refetch on entry, including after settings: the context may predate a
    // successful tune here or on another device.

    const loadGuide = () => {
      const nowMs = Date.now();

      Promise.all([getGuide(GUIDE_HOURS, audienceTier), getProfile(currentUser.id)])
        .then(([data, viewer]) => {
          if (cancelled) return;
          // Commercial breaks are real, timed entries the schedule engine
          // tracks for playback purposes, but a cable guide never lists
          // them as their own row — drop them here, at the one place data
          // comes in, so every downstream index (keyboard nav, focus/tune,
          // rendering) only ever deals with actual programs. Then
          // un-fragment any single movie/episode ModeInterval broke into
          // several short segments (see mergeIntervalSegments) — also
          // here, so nothing downstream ever needs to know segments
          // existed at all.
          const sortedData = data
            .map((c) => ({ ...c, programs: mergeIntervalSegments(c.programs.filter((p) => p.kind !== 'commercial')), previousPrograms: mergeIntervalSegments((c.previousPrograms ?? []).filter(p => p.kind !== 'commercial')) }))
            .sort((a, b) => a.number - b.number);
          setChannels(sortedData);
          setNowMs(Date.now());
          setLoadError(null);
          setFocused((sel) => reconcileSelection(sel, sortedData, nowMs));
          setTuned((sel) => reconcileSelection(sel, sortedData, nowMs));

          // Restore an initial channel whenever the audience tier changes
          // (including the first load). Profile switches remount this page. A
          // channel with nothing scheduled yet (most default channels,
          // until library content is classified — see internal/lineup)
          // can't be focused, since there's no program to point at. Land
          // on whatever's actually live for it (see currentProgramIndex),
          // not array index 0.
          if (initialFocusTierRef.current !== audienceTier) {
            // Explicit links win; otherwise restore the server's channel identity.
            // The filtered guide excludes hidden/ineligible channels and empty rows.
            const firstWithContent =
              sortedData.find((c) => c.number === urlWantedRef.current && c.programs.length > 0) ??
              sortedData.find((c) => c.channelId === viewer.lastWatchedChannelId && c.programs.length > 0) ??
              sortedData.find((c) => c.programs.length > 0);
            if (firstWithContent) {
              urlWantedRef.current = null;
              initialFocusTierRef.current = audienceTier;
              const cell = { channelNumber: firstWithContent.number, programStartsAt: currentProgramStartsAt(firstWithContent.programs, nowMs) };
              setFocused(cell);
              setTuned(cell);
            }
          }
        })
        .catch((err: Error) => {
          if (!cancelled) setLoadError(err.message);
        });
    };

    loadGuide();
    // The schedule keeps moving underneath this same view (the current
    // slot shifts, breaks get inserted/removed, admin settings changes
    // regenerate it server-side) — re-poll on an interval rather than
    // only ever fetching once on mount, so a guide left open stays live
    // instead of looking frozen/stale.
    const intervalId = window.setInterval(loadGuide, GUIDE_POLL_INTERVAL_MS);
    window.addEventListener('focus', loadGuide);
    document.addEventListener('visibilitychange', loadGuide);
    return () => {
      cancelled = true;
      window.clearInterval(intervalId);
      window.removeEventListener('focus', loadGuide);
      document.removeEventListener('visibilitychange', loadGuide);
    };
  }, [currentUser.id, audienceTier, startupCompletions]);

  // Memoized (not just `channels ?? []` inline) so slotPixelWidth's own
  // useMemo below can depend on it without recomputing every render —
  // `channels` only actually changes on a real data fetch.
  const sorted = useMemo(() => channels ?? [], [channels]);
  const guideHasContent = sorted.some((c) => c.programs.length > 0);
  const startupRunning = !guideHasContent && startupStatus !== null && isBusy(startupStatus);

  const slotLabels = Array.from({ length: slotCount }, (_, i) => {
    if (gridStartMs === null) return '';
    const d = new Date(gridStartMs + i * SLOT_MINUTES * 60_000);
    return formatClock(d.getHours() * 60 + d.getMinutes(), timeFormat);
  });

  // RowMetrics (font/height, driven only by font size — never
  // by any one program) come from real hidden elements sharing the
  // exact classes a visible box's lines use (rendered once below), so
  // this always matches actual rendering exactly. Re-measured whenever
  // something that could change font metrics does.
  const titleMeasureRef = useRef<HTMLSpanElement>(null);
  const episodeMeasureRef = useRef<HTMLSpanElement>(null);
  const timeMeasureRef = useRef<HTMLSpanElement>(null);
  const [metrics, setMetrics] = useState<RowMetrics | null>(null);
  useLayoutEffect(() => {
    const titleEl = titleMeasureRef.current;
    const episodeEl = episodeMeasureRef.current;
    const timeEl = timeMeasureRef.current;
    if (!titleEl || !episodeEl || !timeEl) return;
    setMetrics({
      titleFont: getComputedStyle(titleEl).font,
      episodeFont: getComputedStyle(episodeEl).font,
      timeFont: getComputedStyle(timeEl).font,
      titleHeight: titleEl.getBoundingClientRect().height,
      episodeHeight: episodeEl.getBoundingClientRect().height,
      timeHeight: timeEl.getBoundingClientRect().height,
    });
  }, []);

  // Every row needs to be at least tall enough for its 3 stacked lines
  // at the viewer's current font metrics — content-independent (unlike
  // width), so one shared value covers every row.
  const rowMinHeight = requiredRowMinHeight(metrics);

  // Reserve room for the channel number and wrapped name, scaled with row text.
  const infoWidth = rowMinHeight * 2;
  const channelColWidth = infoWidth;

  // A column is 30 minutes and a chunk (20 minutes) is twice the time line's text
  // width, so 1 minute = chunk / 20 px. Whole pixels: fractional box positions have
  // made Chromium on Linux garble the text inside.
  const slotPixelWidth = metrics
    ? Math.round((2 * (measureTextWidth(metrics.timeFont, chunkReferenceText(timeFormat)) + TEXT_MEASURE_BUFFER) * SLOT_MINUTES) / CHUNK_MINUTES)
    : FALLBACK_SLOT_WIDTH;

  // Every box sits at its true time inside a strip exactly as wide as the
  // header's slot columns combined, so boxes always line up under their columns.
  const timelineWidthPx = slotCount * slotPixelWidth;
  // The header uses one real column per slot (each exactly one slot
  // wide, so integer grid tracks are exact here — no fractional-span
  // issue like the per-program boxes below). Program rows use only two
  // columns — the channel col, then a single fixed-width strip normally
  // exactly as wide as all the header's slot columns combined (see
  // timelineWidthPx above) — so a program's
  // pixel position inside that strip (see GuideChannelRow) lines up with
  // the header regardless of its own real, non-slot-aligned duration.
  const gridTemplateColumns = `${channelColWidth}px repeat(${slotCount}, ${slotPixelWidth}px)`;
  const rowGridTemplateColumns = `${channelColWidth}px ${timelineWidthPx}px`;

  // Keyboard nav works immediately on load, with no click needed first.
  useEffect(() => {
    scrollRef.current?.focus();
  }, []);

  useLayoutEffect(() => {
    if (!focused) return;
    const key = `${focused.channelNumber}-${focused.programStartsAt}`;
    const el = slotRefs.current.get(key);
    const container = scrollRef.current;
    if (!el || !container) return;

    // Native scrollIntoView doesn't know part of this container is
    // visually covered by our own sticky header row and sticky channel
    // column — it only sees plain container geometry. That's why it
    // could treat a box as "already visible" (or align it to a spot)
    // that's actually hidden underneath our overlays: the previous
    // "inline: nearest" and "inline: start" attempts were both fooled by
    // this in different ways (a wide program box rendering with its
    // title tucked behind the channel column, or the scroll never quite
    // returning to the true edge). Computing the real, rendered visible
    // rect ourselves and scrolling the minimum needed to clear it fixes
    // both, because it's based on actual geometry, not a heuristic.
    const elRect = el.getBoundingClientRect();
    const containerRect = container.getBoundingClientRect();
    const headerHeight = headRef.current?.getBoundingClientRect().height ?? 0;

    const visibleLeft = containerRect.left + channelColWidth;
    const visibleRight = containerRect.right;
    const visibleTop = containerRect.top + headerHeight;
    const visibleBottom = containerRect.bottom;

    // A program box can be wider than the visible time window can ever
    // show at once (e.g. a 3-hour movie in a 2-hour view) — such a box's
    // right edge is *always* past visibleRight, so unconditionally
    // chasing it would scroll right, push the box's start (and its
    // title) out of view on the left instead, and then chase THAT on the
    // next run: an oscillation that never settles. Always prioritize the
    // start being reachable; only also pull the right edge in when the
    // whole box actually fits within the visible width.
    let deltaX = 0;
    if (elRect.left < visibleLeft || elRect.left > visibleRight) {
      deltaX = elRect.left - visibleLeft;
    } else if (elRect.right > visibleRight && elRect.right - elRect.left <= visibleRight - visibleLeft) {
      deltaX = elRect.right - visibleRight;
    }

    let deltaY = 0;
    if (elRect.top < visibleTop) deltaY = elRect.top - visibleTop;
    else if (elRect.bottom > visibleBottom) deltaY = elRect.bottom - visibleBottom;

    // Hard stop at the true edges, instead of trusting the geometry-based
    // delta above to land exactly on 0: that delta is only ever "the
    // minimum scroll needed to reveal this one box," so repeatedly
    // arrowing left/up through boxes of slightly different measured
    // widths/heights (rounding in layoutPrograms, a row's own content
    // driving its measured height) can accumulate a few stray pixels
    // short of the true origin — the guide then never quite returns to
    // its starting position. Snapping to exactly 0 whenever focus is
    // genuinely at the first program/first focusable channel row makes
    // "hold left" and "hold up" always bottom out at the real edge.
    const focusedChannelForEdge = sorted.find((c) => c.number === focused.channelNumber);
    const atLeftEdge = focusedChannelForEdge?.programs[0]?.startsAt === focused.programStartsAt;
    const firstFocusableChannelNumber = sorted.find((c) => c.programs.length > 0)?.number;
    const atTopEdge = focused.channelNumber === firstFocusableChannelNumber;

    const targetScrollLeft = atLeftEdge ? 0 : container.scrollLeft + deltaX;
    const targetScrollTop = atTopEdge ? 0 : container.scrollTop + deltaY;
    if (targetScrollLeft !== container.scrollLeft || targetScrollTop !== container.scrollTop) {
      container.scrollTo({ left: targetScrollLeft, top: targetScrollTop, behavior: 'auto' });
    }
    // Also re-run whenever column geometry changes (window resize, or a
    // "time span shown" setting change): the previously-focused cell's
    // on-screen position shifts even though `focused` itself didn't
    // change, so without this the scroll position goes stale and the
    // cell ends up rendering behind the sticky header/channel column.
    // `sorted` is included so a poll that changes which channel is first
    // re-evaluates atTopEdge too.
  }, [focused, slotPixelWidth, sorted, metrics, channelColWidth]);

  const previousGridStartMs = useRef(gridStartMs);
  useLayoutEffect(() => {
    const container = scrollRef.current;
    if (!container) return;
    if (previousGridStartMs.current !== gridStartMs) container.scrollLeft = 0;
    previousGridStartMs.current = gridStartMs;
    if (fullscreen || !restoreGuideRef.current || !tuned) return;
    const cell = slotRefs.current.get(`${tuned.channelNumber}-${tuned.programStartsAt}`);
    const row = cell?.closest('.guide-row');
    if (!row) return;
    restoreGuideRef.current = false;
    setFocused(tuned);
    container.scrollLeft = 0;
    container.scrollTop += row.getBoundingClientRect().top - container.getBoundingClientRect().top
      - (headRef.current?.getBoundingClientRect().height ?? 0);
    container.focus({ preventScroll: true });
  }, [fullscreen, guideEntry, tuned, gridStartMs, metrics]);

  // The info/preview text tracks whatever's *focused* (the keyboard/click
  // cursor), separately from `tuned` — browsing ahead into the future
  // should let a viewer read about it without acting like they actually
  // tuned there; see the Enter/click handlers below, which always
  // resolve `tuned` to whatever's actually live on the target channel
  // via currentProgramIndex, never to the clicked/focused slot itself.
  const focusedChannel = focused ? (sorted.find((c) => c.number === focused.channelNumber) ?? null) : null;
  const focusedProgram = focused && focusedChannel ? (focusedChannel.programs.find((p) => p.startsAt === focused.programStartsAt) ?? null) : null;

  // IMPORTANT: every branch derives its result from the *previous* state
  // passed into the updater (never from the `focused` in this render's
  // closure). Holding/rapidly pressing arrow keys fires many keydown
  // events faster than React can re-render in between, so a version that
  // read the outer `focused`/`channel` could compute a result valid for
  // one channel but stale for whichever channel a queued-but-not-yet-
  // rendered update had already switched to — indexing out of bounds on
  // the next keypress and crashing the render (this was reproducible:
  // it showed up as a blank page in a screen recording).
  const handleKeyDown = (e: KeyboardEvent) => {
    if (!focused || e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey) return;
    const target = e.target instanceof Element ? e.target : null;
    // The profile menu own the keyboard while they are in use.
    if (target?.closest('select, input:not([type="range"]), textarea, dialog, .profile-menu')) return;
    // Enter and Space still press a focused button rather than tuning.
    if (e.key === 'Enter' && target?.closest('button, a, summary')) return;
    // Expanded playback reserves horizontal arrows for its control cursor.
    if (playerLarge || fullscreen) {
      if (document.querySelector('.playback-menu')) return;
      const step = e.key === 'ArrowDown' ? 1 : e.key === 'ArrowUp' ? -1 : 0;
      if (step !== 0) {
        e.preventDefault();
        // The ref, not state, so a burst of key presses keeps stepping.
        const from = sorted.findIndex((c) => c.number === (tunedNow.current ?? focused).channelNumber);
        const i = circularChannelIndex(sorted, from, step);
        if (i !== null) {
          const startsAt = currentProgramStartsAt(sorted[i].programs, Date.now());
          tunedNow.current = { channelNumber: sorted[i].number, programStartsAt: startsAt };
          urlWantedRef.current = null;
          setTuned(tunedNow.current);
          setFocused(tunedNow.current);
        }
        return;
      }
      if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') return;
    }
    if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
      e.preventDefault();
      const delta = e.key === 'ArrowRight' ? 1 : -1;
      setFocused((prev) => {
        if (!prev) return prev;
        const channel = sorted.find((c) => c.number === prev.channelNumber);
        if (!channel || channel.programs.length === 0) return prev;
        // Re-resolve prev's own index fresh against the *current* array
        // every keypress, rather than trusting a stored index — a poll
        // landing between keypresses can shift what index prev's program
        // now sits at (or age it out of the array entirely; findIndex
        // then returns -1, which the Math.max below just treats as "walk
        // from the start," a reasonable recovery rather than a crash).
        const currentIndex = channel.programs.findIndex((p) => p.startsAt === prev.programStartsAt);
        const maxIndex = channel.programs.length - 1;
        const nextIndex = Math.max(0, Math.min(maxIndex, currentIndex + delta));
        const nextStartsAt = channel.programs[nextIndex].startsAt;
        // Already at the first/last program in this row: bail out with the
        // exact same object reference. Returning a same-valued-but-new
        // object here would still count as a state change to React (and
        // re-run the scroll-correction effect below on an already-settled
        // scroll position) purely from pressing further into a wall.
        if (nextStartsAt === prev.programStartsAt) return prev;
        return { ...prev, programStartsAt: nextStartsAt };
      });
    } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      setFocused((prev) => {
        if (!prev) return prev;
        const channelIdx = sorted.findIndex((c) => c.number === prev.channelNumber);
        const channel = sorted[channelIdx];
        if (!channel) return prev;
        const currentProgram = channel.programs.find((p) => p.startsAt === prev.programStartsAt);

        // Skip channels with nothing scheduled — there's no program to
        // land the cursor on there.
        const nextIdx = circularChannelIndex(sorted, channelIdx, e.key === 'ArrowDown' ? 1 : -1);
        if (nextIdx === null) return prev;

        const nextChannel = sorted[nextIdx];
        const currentStart = currentProgram ? new Date(currentProgram.startsAt).getTime() : 0;
        const found = nextChannel.programs.findIndex((p) => {
          const start = new Date(p.startsAt).getTime();
          const end = new Date(p.endsAt).getTime();
          return start <= currentStart && currentStart < end;
        });
        return { channelNumber: nextChannel.number, programStartsAt: nextChannel.programs[found >= 0 ? found : 0].startsAt };
      });
    } else if (e.key === 'Enter') {
      e.preventDefault();
      // Route through setFocused's updater (not the outer `focused`) so
      // this always tunes to whatever the *actual* latest focus is, even
      // if an arrow-key update from the same burst hasn't rendered yet.
      // Tuning always means the channel's own live show — never the
      // specific past/future slot the cursor happens to be sitting on
      // (see currentProgramIndex); a channel can't actually be changed
      // to play something else, so selecting a slot two hours back only
      // ever moves the focus cursor there to read about it.
      setFocused((prev) => {
        if (prev) {
          const channel = sorted.find((c) => c.number === prev.channelNumber);
          if (channel && channel.programs.length > 0) {
            urlWantedRef.current = null;
            setTuned({ channelNumber: prev.channelNumber, programStartsAt: currentProgramStartsAt(channel.programs, Date.now()) });
          }
        }
        return prev;
      });
    }
  };

  // The whole page belongs to the guide, wherever focus happens to be.
  const keyHandler = useRef(handleKeyDown);
  useEffect(() => {
    keyHandler.current = handleKeyDown;
  });
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => keyHandler.current(e);
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  return (
    <div className={'guide-page' + (playerLarge ? ' is-large-player' : '')} ref={pageRef}>
      <div className="page-header" ref={pageHeaderRef}>
        <div className="page-header-brand"><Brand /></div>
        <div className="page-header-actions">
          <ProfileMenu />
        </div>
      </div>

      <div className={'preview-section' + (playerLarge ? ' is-large' : '')}>
        <div className="preview-detail">
          {focusedChannel && focusedProgram ? (
            <>
              <div className="preview-detail-main">
                <div className="preview-channel">
                  <span className="preview-channel-badge">{focusedChannel.number}</span>
                  <span className="preview-channel-name">{focusedChannel.name}</span>
                </div>
                <div className="preview-title">{displayTitle(focusedProgram)}</div>
                {focusedProgram.mediaType !== 'movie' && episodeLabel(focusedProgram) && <div className="preview-episode">{episodeLabel(focusedProgram)}</div>}
                {ratingLabel(focusedProgram) && (
                  <div className="preview-meta" title="Age Restrictions">
                    {ratingLabel(focusedProgram)}
                  </div>
                )}
                {formatAirDate(focusedProgram.airDate) && (
                  <div className="preview-meta" title="Original Air Date">
                    {formatAirDate(focusedProgram.airDate)}
                  </div>
                )}
              </div>
              <div className="preview-description">
                <div>{focusedProgram.overview}</div>
                <div className="preview-time">{formatRange(clockMinutesOf(focusedProgram.startsAt), clockMinutesOf(focusedProgram.endsAt), timeFormat)}</div>
              </div>
            </>
          ) : (
            <div className="preview-detail-main">
              <div className="preview-title">
                {loadError
                  ? `Couldn't load the guide: ${loadError}`
                  : startupRunning
                    ? 'Tuning…'
                    : channels === null
                      ? 'Loading guide…'
                      : 'Nothing scheduled yet'}
              </div>
            </div>
          )}
        </div>
        <div className="preview-video-wrap" style={{ width: playerLarge ? undefined : DEFAULT_PREVIEW_WIDTH }}>
          <div className="preview-video">
            <Player
              channelId={sorted.find((c) => c.number === tuned?.channelNumber)?.channelId ?? null}
              large={playerLarge}
              onToggleLarge={mobilePlayer ? undefined : () => {
                if (largePlayer) {
                  restoreGuideRef.current = true;
                  setNowMs(Date.now());
                  setGuideEntry(value => value + 1);
                }
                setLargePlayer(value => !value);
              }}
            />
          </div>
        </div>
      </div>

      {/* Off-screen, never painted visibly — exists purely so RowMetrics
          can read real computed font/height for these exact classes
          (see the useLayoutEffect above). Kept outside guide-section so
          its own layout can't affect the real grid. */}
      <div aria-hidden="true" style={{ position: 'absolute', visibility: 'hidden', pointerEvents: 'none', whiteSpace: 'nowrap', left: -99999, top: -99999 }}>
        <span ref={titleMeasureRef} className="guide-slot-title">
          Sample
        </span>
        <span ref={episodeMeasureRef} className="guide-slot-episode">
          Sample
        </span>
        <span ref={timeMeasureRef} className="guide-slot-time">
          Sample
        </span>
      </div>

      <div className="guide-section">
        {!guideHasContent && <StartupNotice status={startupStatus} variant="panel" />}
        <div className="guide-scroll no-scrollbar" ref={scrollRef} tabIndex={0}>
          <div className="guide-head-row" ref={headRef} style={{ gridTemplateColumns }}>
            <div className="guide-col-channel guide-head-channel"><GuideClock format={timeFormat} /></div>
            {slotLabels.map((label, i) => (
              <div key={i} className="guide-head-slot" data-start-ms={(gridStartMs ?? 0) + i * SLOT_MINUTES * 60_000}>
                {label}
              </div>
            ))}
          </div>
          {gridStartMs !== null &&
            sorted.map((channel) => (
              <GuideChannelRow
                key={channel.number}
                channel={channel}
                gridStartMs={gridStartMs}
                timeFormat={timeFormat}
                gridTemplateColumns={rowGridTemplateColumns}
                slotPixelWidth={slotPixelWidth}
                rowMinHeight={rowMinHeight}
                infoWidth={infoWidth}
                timelineWidthPx={timelineWidthPx}
                timeFont={metrics?.timeFont}
                focused={focused}
                tuned={tuned}
                slotRefs={slotRefs}
                onSelectProgram={(programStartsAt) => {
                  // Same rule as Enter (see handleKeyDown): clicking any
                  // slot moves the focus cursor there, but only ever
                  // tunes to whatever's actually live on that channel.
                  setFocused({ channelNumber: channel.number, programStartsAt });
                  if (channel.programs.length > 0) {
                    urlWantedRef.current = null;
                    setTuned({ channelNumber: channel.number, programStartsAt: currentProgramStartsAt(channel.programs, Date.now()) });
                  }
                }}
              />
            ))}
          <div aria-hidden="true" style={{ height: `max(0px, calc(100% - ${rowMinHeight}px - 40px))` }} />
        </div>
      </div>
    </div>
  );
}

interface SlotLayout {
  leftPx: number;
  widthPx: number;
}

// A box sits at its true time: left edge at its start, width equal to its
// runtime, so it lines up under the header's slot columns. A program already
// airing at the grid's left edge is clipped to it, and one running past the
// last column is clipped to the strip. Short programs are narrow slivers (their
// text is cut off; the tooltip has it all) rather than pushing later ones aside.
function layoutPrograms(programs: GuideProgram[], gridStartMs: number, slotPixelWidth: number, timelineWidthPx: number): SlotLayout[] {
  const pxPerMs = slotPixelWidth / (SLOT_MINUTES * 60_000);
  return programs.map((program) => {
    const startPx = Math.max(0, (new Date(program.startsAt).getTime() - gridStartMs) * pxPerMs);
    const endPx = Math.min(timelineWidthPx, (new Date(program.endsAt).getTime() - gridStartMs) * pxPerMs);
    // Whole pixels: fractional left/width has made Chromium on Linux garble the text inside.
    const leftPx = Math.round(startPx) + 3;
    return { leftPx, widthPx: Math.max(Math.round(endPx) - 3 - leftPx, 4) };
  });
}

function GuideChannelRow({
  channel,
  gridStartMs,
  timeFormat,
  gridTemplateColumns,
  slotPixelWidth,
  rowMinHeight,
  infoWidth,
  timelineWidthPx,
  timeFont,
  focused,
  tuned,
  slotRefs,
  onSelectProgram,
}: {
  channel: GuideChannel;
  gridStartMs: number;
  timeFormat: TimeFormat;
  gridTemplateColumns: string;
  slotPixelWidth: number;
  rowMinHeight: number;
  infoWidth: number;
  timelineWidthPx: number;
  timeFont?: string;
  focused: Selected | null;
  tuned: Selected | null;
  slotRefs: React.RefObject<Map<string, HTMLButtonElement>>;
  onSelectProgram: (programStartsAt: string) => void;
}) {
  return (
    <div className="guide-row" style={{ gridTemplateColumns, minHeight: rowMinHeight }}>
      <div className="guide-col-channel">
        <div className="guide-channel-info" style={{ flex: `0 0 ${infoWidth}px` }}>
          <span className="guide-channel-num">{channel.number}</span>
          <span className="guide-channel-row-name">{channel.name}</span>
          {channel.audienceTier === 'young_child' && <span className="guide-channel-rating">(kids)</span>}
          {channel.audienceTier === 'teen' && <span className="guide-channel-rating">(teens)</span>}
        </div>
      </div>
      <div className="guide-row-timeline">
        {(() => {
          const history = (channel.previousPrograms ?? []).filter(program => new Date(program.endsAt).getTime() > gridStartMs);
          const displayed = [...history, ...channel.programs.filter(program => Date.parse(program.endsAt) > gridStartMs)];
          const layout = layoutPrograms(displayed, gridStartMs, slotPixelWidth, timelineWidthPx);
          return displayed.map((program, index) => {
            const { leftPx, widthPx } = layout[index];
            if (index < history.length) {
              const range = formatRange(clockMinutesOf(program.startsAt), clockMinutesOf(program.endsAt), timeFormat);
              const showText = !!timeFont && measureTextWidth(timeFont, range) <= widthPx - 22;
              return <div key={`past-${program.startsAt}`} className="guide-slot-history" style={{left: leftPx, width: widthPx, padding: showText ? undefined : 0}}
                data-starts-at={program.startsAt} data-ends-at={program.endsAt} title={[displayTitle(program), episodeLabel(program), range].filter(Boolean).join(' · ')}>
                {showText && <><span className="guide-slot-title">{displayTitle(program)}</span><span className="guide-slot-episode">{episodeLabel(program)}</span><span className="guide-slot-time">{range}</span></>}
              </div>;
            }
            const isFocused = channel.number === focused?.channelNumber && program.startsAt === focused.programStartsAt;
            const isTuned = channel.number === tuned?.channelNumber && program.startsAt === tuned.programStartsAt;
            // Keyed by the program's own identity, not its index: an
            // index-based key would let React reuse the same DOM node
            // (and this same ref) across a re-fetch that shifts what's
            // at that position (merging, or a program aging out of the
            // fetched window) as if it were still the same box.
            const key = `${channel.number}-${program.startsAt}`;
            return (
              <button
                key={key}
                ref={(el) => {
                  if (el) slotRefs.current.set(key, el);
                  else slotRefs.current.delete(key);
                }}
                className={'guide-slot-box' + (isTuned ? ' guide-slot-box-tuned' : '') + (isFocused ? ' guide-slot-box-focused' : '')}
                style={{ left: leftPx, width: widthPx }}
                title={[displayTitle(program), episodeLabel(program), formatRange(clockMinutesOf(program.startsAt), clockMinutesOf(program.endsAt), timeFormat)]
                  .filter(Boolean)
                  .join(' · ')}
                data-starts-at={program.startsAt}
                data-ends-at={program.endsAt}
                onClick={() => onSelectProgram(program.startsAt)}
              >
                <span className="guide-slot-title">{displayTitle(program)}</span>
                <span className="guide-slot-episode">{episodeLabel(program)}</span>
                <span className="guide-slot-time">{formatRange(clockMinutesOf(program.startsAt), clockMinutesOf(program.endsAt), timeFormat)}</span>
              </button>
            );
          });
        })()}
      </div>
    </div>
  );
}
