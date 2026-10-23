import { useEffect, useState } from "react";

// Touch capability also identifies iPad Safari in desktop browsing mode.
// Classification must not change when fullscreen resizes the viewport.
export function isTouchBrowser() {
  return navigator.maxTouchPoints > 0 || window.matchMedia("(pointer: coarse)").matches;
}

export function useTouchLayout() {
  const read = () => ({
    touch: isTouchBrowser(),
    portrait: window.matchMedia("(orientation: portrait)").matches,
  });
  const [layout, setLayout] = useState(read);
  useEffect(() => {
    const query = window.matchMedia("(orientation: portrait)");
    const sync = () => setLayout(read());
    query.addEventListener("change", sync);
    window.addEventListener("resize", sync);
    return () => {
      query.removeEventListener("change", sync);
      window.removeEventListener("resize", sync);
    };
  }, []);
  return layout;
}
