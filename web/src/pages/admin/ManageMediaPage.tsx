import { useEffect, useRef, useState } from "react";
import LibraryCatalog from "./LibraryCatalog";
import "./AdminShared.css";

export default function ManageMediaPage({ filterTarget }: { filterTarget: HTMLElement | null }) {
  const ref = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState("movies");
  useEffect(() => {
    // Switch when TV reaches the upper half of the viewport; keep both
    // catalogs mounted so their independent filters and selections survive.
    const update = () => {
      const shows = ref.current?.querySelector('[data-library-section="shows"]');
      setActive(window.scrollY > 0 && shows && shows.getBoundingClientRect().top <= window.innerHeight / 2 ? "shows" : "movies");
    };
    update();
    window.addEventListener("scroll", update, { passive: true });
    window.addEventListener("resize", update);
    const observer = new ResizeObserver(update);
    if (ref.current) observer.observe(ref.current);
    return () => {
      window.removeEventListener("scroll", update);
      window.removeEventListener("resize", update);
      observer.disconnect();
    };
  }, []);
  return (
    <div className="admin-section manage-media-page" ref={ref}>
      <LibraryCatalog filterTarget={filterTarget} view="movies" active={active === "movies"} />
      <LibraryCatalog filterTarget={filterTarget} view="shows" active={active === "shows"} />
    </div>
  );
}
