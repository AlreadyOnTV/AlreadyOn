import { useEffect, useState } from "react";

export function isTouchBrowser() {
  return navigator.maxTouchPoints > 0 || window.matchMedia("(pointer: coarse)").matches;
}

export function isAppleTouchBrowser() {
  return isIOSChrome() || /iPad|iPhone|iPod/.test(navigator.userAgent + " " + navigator.platform) ||
    (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
}

export function isIOSChrome() {
  return /CriOS\//.test(navigator.userAgent);
}

export function readTouchLayout() {
  const touch = isTouchBrowser();
  // iPad desktop mode identifies as a Mac. Screen size alone must not turn it
  // into a phone when a browser reports a narrow desktop-site viewport.
  const ipad = /iPad/.test(navigator.userAgent) ||
    (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  const phone = touch && !ipad &&
    Math.min(window.screen.width, window.screen.height) < 600;
  const angle = (window as Window & { orientation?: number }).orientation;
  // Apple browser viewport and ScreenOrientation updates can disagree during
  // rotation/fullscreen. Use the device angle for both layout and fullscreen.
  const portrait = touch && isAppleTouchBrowser() && typeof angle === "number"
    ? Math.abs(angle) % 180 === 0
    : touch && (isAppleTouchBrowser() || /Android/.test(navigator.userAgent)) &&
        window.screen.orientation?.type
      ? window.screen.orientation.type.startsWith("portrait")
      : window.matchMedia("(orientation: portrait)").matches;
  return { touch, phone, portrait };
}

export function useTouchLayout() {
  const [layout, setLayout] = useState(readTouchLayout);
  useEffect(() => {
    const query = window.matchMedia("(orientation: portrait)");
    const sync = () => setLayout(readTouchLayout());
    query.addEventListener("change", sync);
    window.screen.orientation?.addEventListener("change", sync);
    window.addEventListener("orientationchange", sync);
    window.addEventListener("resize", sync);
    window.visualViewport?.addEventListener("resize", sync);
    return () => {
      query.removeEventListener("change", sync);
      window.screen.orientation?.removeEventListener("change", sync);
      window.removeEventListener("orientationchange", sync);
      window.removeEventListener("resize", sync);
      window.visualViewport?.removeEventListener("resize", sync);
    };
  }, []);
  return layout;
}

export function useIOSChromeViewport() {
  useEffect(() => {
    if (!isIOSChrome()) return;
    const viewport = document.querySelector('meta[name="viewport"]');
    const original = viewport?.getAttribute("content");
    if (!viewport || original == null) return;
    // With initial-scale alone WebKit infers device-height as landscape width,
    // instead of retaining a portrait device-width in Chrome's embedded view.
    // Keep pinch zoom available and leave Safari's working configuration alone.
    const content = "initial-scale=1.0, viewport-fit=cover";
    viewport.setAttribute("content", content);
    return () => {
      if (viewport.getAttribute("content") === content)
        viewport.setAttribute("content", original);
    };
  }, []);
}
