import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { readTouchLayout, useTouchLayout } from "./touchLayout";

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
  const ownsNative = useCallback(() => document.fullscreenElement === stage.current ||
    !!(video.current as NativeVideo | null)?.webkitDisplayingFullscreen, [stage, video]);
  const exitNative = useCallback(() => {
    if (document.fullscreenElement === stage.current)
      void document.exitFullscreen().catch(() => {});
    const element = video.current as NativeVideo | null;
    if (element?.webkitDisplayingFullscreen) element.webkitExitFullscreen?.();
  }, [stage, video]);
  const enterNative = useCallback(async () => {
    if (ownsNative()) return;
    const element = video.current as NativeVideo | null;
    // Keep one capability-based path for every browser: stage fullscreen
    // preserves our controls; native video fullscreen is the fallback.
    if (stage.current?.requestFullscreen) await stage.current.requestFullscreen();
    else element?.webkitEnterFullscreen?.();
  }, [stage, video, ownsNative]);
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
    const controller = new AbortController();
    // Apply the orientation transition on the next frame, after the DOM layout
    // has caught up. Cleanup cancels both the frame and stale native entry.
    const frame = window.requestAnimationFrame(() => {
      if (layout.portrait) {
        dismissed.current = false;
        setViewportFullscreen(false);
        // Manual fullscreen from portrait stays open until device rotation.
        exitNative();
      } else if (layout.phone && !dismissed.current) {
        setViewportFullscreen(true);
        void enterNative().then(() => {
          if (controller.signal.aborted && readTouchLayout().portrait) exitNative();
        }).catch(() => {
          // Automatic rotation may lack user activation; retain the viewport fallback.
        });
      }
    });
    return () => { controller.abort(); window.cancelAnimationFrame(frame); };
  }, [layout.touch, layout.phone, layout.portrait, active, enterNative, exitNative]);

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
