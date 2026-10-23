import { useEffect, useRef, useState, type RefObject } from "react";
import { isIOSChrome, readTouchLayout, useTouchLayout } from "./touchLayout";

type NativeVideo = HTMLVideoElement & {
  webkitDisplayingFullscreen?: boolean;
  webkitEnterFullscreen?: () => void;
  webkitExitFullscreen?: () => void;
};

export function usePlayerFullscreen(
  stage: RefObject<HTMLDivElement | null>,
  video: RefObject<HTMLVideoElement | null>,
  active: boolean,
) {
  const layout = useTouchLayout();
  const [viewportFullscreen, setViewportFullscreen] = useState(false);
  const dismissed = useRef(false);
  const nativeWasActive = useRef(false);
  const ownsNative = () => document.fullscreenElement === stage.current ||
    !!(video.current as NativeVideo | null)?.webkitDisplayingFullscreen;
  const exitNative = () => {
    if (document.fullscreenElement === stage.current)
      void document.exitFullscreen().catch(() => {});
    const element = video.current as NativeVideo | null;
    if (element?.webkitDisplayingFullscreen) element.webkitExitFullscreen?.();
  };
  const enterNative = async () => {
    if (ownsNative()) return;
    const element = video.current as NativeVideo | null;
    // Chrome's iOS embedded view can advertise container fullscreen but tear
    // it down immediately. Use its native video player directly when available.
    if (isIOSChrome() && element?.webkitEnterFullscreen) element.webkitEnterFullscreen();
    else if (stage.current?.requestFullscreen) await stage.current.requestFullscreen();
    else element?.webkitEnterFullscreen?.();
  };
  useEffect(() => {
    const element = video.current;
    const sync = () => {
      const native = document.fullscreenElement === stage.current ||
        !!(element as NativeVideo | null)?.webkitDisplayingFullscreen;
      // Native Done/Escape must also dismiss the phone viewport fallback, and
      // resize events must not immediately re-enter while it is held landscape.
      if (nativeWasActive.current && !native && readTouchLayout().phone &&
          !readTouchLayout().portrait) {
        dismissed.current = true;
        setViewportFullscreen(false);
      }
      nativeWasActive.current = native;
    };
    document.addEventListener("fullscreenchange", sync);
    element?.addEventListener("webkitbeginfullscreen", sync);
    element?.addEventListener("webkitendfullscreen", sync);
    return () => {
      document.removeEventListener("fullscreenchange", sync);
      element?.removeEventListener("webkitbeginfullscreen", sync);
      element?.removeEventListener("webkitendfullscreen", sync);
    };
  }, [stage, video]);
  useEffect(() => {
    if (!layout.touch || !active) return;
    let cancelled = false;
    if (layout.portrait) {
      dismissed.current = false;
      setViewportFullscreen(false);
      // This effect follows device orientation, never fullscreen viewport size.
      // Manual fullscreen from portrait therefore stays open until rotation.
      exitNative();
    } else if (layout.phone && !dismissed.current) {
      setViewportFullscreen(true);
      void enterNative().then(() => {
        if (cancelled && readTouchLayout().portrait) exitNative();
      }).catch(() => {
        // Rotation need not carry a browser user activation. The viewport
        // fallback still provides fullscreen playback if native entry is denied.
      });
    }
    return () => { cancelled = true; };
    // Native calls read stable DOM refs; only device rotation or tuning from an
    // empty player should trigger automatic fullscreen, never a media resize.
  }, [layout.touch, layout.phone, layout.portrait, active]);

  const toggleFullscreen = () => {
    if (ownsNative() || viewportFullscreen) {
      dismissed.current = layout.phone && !layout.portrait;
      setViewportFullscreen(false);
      exitNative();
      return false;
    } else {
      dismissed.current = false;
      if (layout.phone && !layout.portrait) setViewportFullscreen(true);
      void enterNative().catch(() => {});
      return true;
    }
  };
  return { viewportFullscreen, toggleFullscreen };
}
