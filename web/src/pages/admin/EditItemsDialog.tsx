// Explicit unchanged controls prevent bulk edits from copying representative values.
import { useEffect, useRef, useState } from "react";
import {
  lookupTMDbItem,
  previewTMDbItem,
  getCertifications,
  getGenres,
  updateCatalog,
  type CatalogItem,
  type CatalogView,
  type Genre,
  type LibraryItemPatch,
  type TMDbPreview,
  type UnmatchedItem,
} from "../../api/client";
import InfoModal from "./InfoModal";
import MultiSelectDropdown from "./MultiSelectDropdown";

type EditableItem = UnmatchedItem & Partial<CatalogItem>;
const audienceName = (value?: string) =>
  ({ young_child: "Kids", teen: "Teens", adult: "Unrestricted" })[
    value ?? ""
  ] ?? "Automatic";
const commonGenres = (items: EditableItem[]): string[] => {
  const firstGenres = items[0]?.genres ?? [];
  return firstGenres.filter((genre) =>
    items.every((item) => item.genres?.includes(genre)),
  );
};

export default function EditItemsDialog({
  items,
  view: requestedView,
  onClose,
  onSaved,
}: {
  items: EditableItem[];
  view?: Extract<CatalogView, "movies" | "shows">;
  onClose: () => void;
  onSaved: (updated: number) => void;
}) {
  const reviewItems = items.filter((item) => item.needsReview);
  const single = items.length === 1;
  const first = items[0];
  const view =
    requestedView ?? (first.mediaType === "movie" ? "movies" : "shows");
  const noun =
    view === "shows" ? "show" : view === "movies" ? "movie" : "episode";
  const [genres, setGenres] = useState<Genre[]>([]);
  const [certs, setCerts] = useState<{ movie: string[]; tv: string[] }>({
    movie: [],
    tv: [],
  });
  const [title, setTitle] = useState(single ? first.title : "");
  const [year, setYear] = useState(
    single && first.year ? String(first.year) : "",
  );
  const [audience, setAudience] = useState("unchanged");
  const [rating, setRating] = useState("unchanged");
  const [genreIds, setGenreIds] = useState<string[]>(commonGenres(items));
  const [dirty, setDirty] = useState<Set<string>>(new Set());
  const [tmdbId, setTMDbId] = useState(
    single && first.tmdbId ? String(first.tmdbId) : "",
  );
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [genresOpen, setGenresOpen] = useState(false);
  const [tmdbPreview, setTMDbPreview] = useState<TMDbPreview | null>(null);
  const previewedTMDbId = useRef("");
  const previewVersion = useRef(0);
  // Keep the draft from before identity previews, including manual edits, so
  // returning to the saved ID restores it without a provider lookup or write.
  const originalDraft = useRef<{
    title: string;
    year: string;
    genres: string[];
    dirty: Set<string>;
  } | null>(null);
  const pendingPreview = useRef<{
    id: string;
    promise: Promise<boolean>;
  } | null>(null);
  const currentTMDbId = useRef(tmdbId);
  const actionsRef = useRef<HTMLDivElement>(null);
  const touch = (key: string) => setDirty((old) => new Set(old).add(key));

  useEffect(() => {
    const previous = document.body.style.overflow;
    const previousRoot = document.documentElement.style.overflow;
    document.body.style.overflow = "hidden";
    document.documentElement.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previous;
      document.documentElement.style.overflow = previousRoot;
    };
  }, []);

  useEffect(() => {
    getGenres()
      .then(setGenres)
      .catch(() => undefined);
    getCertifications()
      .then(setCerts)
      .catch(() => undefined);
  }, []);

  const previewIdentity = (): Promise<boolean> => {
    const value = tmdbId.trim();
    if (!single || !value) return Promise.resolve(false);
    if (!/^\d+$/.test(value) || Number(value) <= 0) {
      setError("TMDb ID must be a positive number.");
      return Promise.resolve(false);
    }
    if (Number(value) === (first.tmdbId ?? 0)) {
      const original = originalDraft.current;
      if (original) {
        setTitle(original.title);
        setYear(original.year);
        setGenreIds(original.genres);
        setDirty((current) => {
          const next = new Set(current);
          for (const key of ["title", "year", "genres"]) {
            next.delete(key);
            if (original.dirty.has(key)) next.add(key);
          }
          return next;
        });
        originalDraft.current = null;
      }
      setTMDbPreview(null);
      setPreviewing(false);
      setError(null);
      return Promise.resolve(true);
    }
    if (previewedTMDbId.current === value) return Promise.resolve(true);
    if (pendingPreview.current?.id === value)
      return pendingPreview.current.promise;

    const version = previewVersion.current;
    const promise = (async () => {
      setPreviewing(true);
      setError(null);
      try {
        const preview = await previewTMDbItem(first.id, Number(value));
        if (currentTMDbId.current !== value || previewVersion.current !== version) return false;
        originalDraft.current ??= { title, year, genres: genreIds, dirty: new Set(dirty) };
        previewedTMDbId.current = value;
        setTMDbPreview(preview);
        setTitle(preview.title);
        setYear(preview.year ? String(preview.year) : "");
        setGenreIds(preview.genres);
        return true;
      } catch (err) {
        if (currentTMDbId.current === value && previewVersion.current === version) {
          setError(err instanceof Error ? err.message : "TMDb preview failed.");
        }
        return false;
      } finally {
        if (currentTMDbId.current === value && previewVersion.current === version) setPreviewing(false);
      }
    })();
    pendingPreview.current = { id: value, promise };
    void promise.finally(() => {
      if (pendingPreview.current?.promise === promise)
        pendingPreview.current = null;
    });
    return promise;
  };

  const save = async () => {
    const patch: LibraryItemPatch = {};
    if (dirty.has("title")) patch.title = title.trim();
    if (dirty.has("year")) {
      if (year.trim() && !/^\d+$/.test(year.trim())) {
        setError("year must be a whole number.");
        return;
      }
      patch.year = year.trim() ? Number(year) : null;
    }
    if (audience !== "unchanged")
      patch.audienceOverride = audience as "young_child" | "teen" | "adult";
    if (rating !== "unchanged") patch.certification = rating;
    if (dirty.has("genres")) patch.genres = genreIds;
    const identityChanged =
      single && tmdbId.trim() !== "" && Number(tmdbId) !== (first.tmdbId ?? 0);
    if (!Object.keys(patch).length && !identityChanged) {
      setError("Choose at least one change.");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      if (identityChanged) {
        if (!(await previewIdentity())) {
          setSaving(false);
          return;
        }
        await lookupTMDbItem(first.id, Number(tmdbId));
      }
      const result = Object.keys(patch).length
        ? await updateCatalog(
            items.map((item) => item.id),
            view,
            patch,
          )
        : { updated: 1 };
      onSaved(result.updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save changes.");
      setSaving(false);
    }
  };

  return (
    <InfoModal
      open
      title={
        single
          ? `Edit ${noun}: ${first.title}`
          : `Edit ${items.length} ${noun}s`
      }
      onClose={() => {
        if (!saving) onClose();
      }}
      className="library-edit-dialog"
    >
      {items.length > 1 && (
        <p className="admin-section-hint">
          Changes apply to all {items.length} selected {noun}s. Current values
          remain unchanged unless you select a replacement.
        </p>
      )}
      {first.mixed && (
        <p className="admin-section-hint">
          Episode metadata varies. The library shows the most restrictive
          audience; show-level changes apply to episodes that inherit from this
          show.
        </p>
      )}
      {single && (
        <div className="admin-field-row library-tmdb-field">
          <label htmlFor="library-tmdb">TMDb ID</label>
          <input
            id="library-tmdb"
            type="number"
            min={1}
            value={tmdbId}
            onChange={(event) => {
              const value = event.target.value;
              currentTMDbId.current = value;
              previewVersion.current += 1;
              pendingPreview.current = null;
              setPreviewing(false);
              previewedTMDbId.current = "";
              setTMDbPreview(null);
              setTMDbId(value);
              setError(null);
            }}
            onBlur={() => void previewIdentity()}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                void previewIdentity();
              }
            }}
          />
          {previewing && <span role="status">Looking up…</span>}
        </div>
      )}
      {single && (
        <div className="admin-field-row">
          <label htmlFor="library-edit-title">
            {view === "shows" ? "Show title" : "Title"}
          </label>
          <input
            id="library-edit-title"
            value={title}
            onChange={(e) => {
              setTitle(e.target.value);
              touch("title");
            }}
          />
        </div>
      )}
      <div className="admin-field-row">
        <label htmlFor="library-edit-year">
          {view === "shows" ? "Show premiere year" : "Release year"}
        </label>
        <input
          id="library-edit-year"
          type="number"
          min={1870}
          max={2100}
          value={year}
          placeholder="Leave unchanged"
          onChange={(e) => {
            setYear(e.target.value);
            touch("year");
          }}
        />
      </div>
      <div className="admin-field-row">
        <label htmlFor="library-edit-audience">Intended audience</label>
        <select
          id="library-edit-audience"
          value={audience}
          onChange={(e) => setAudience(e.target.value)}
        >
          <option value="unchanged">
            Current ({single ? audienceName(first.audience) : "varies"})
          </option>
          <option value="young_child">Kids</option>
          <option value="teen">Teens</option>
          <option value="adult">Unrestricted</option>
        </select>
      </div>
      <div className="admin-field-row">
        <label htmlFor="library-edit-rating">Content Rating</label>
        <select
          id="library-edit-rating"
          value={rating}
          onChange={(e) => setRating(e.target.value)}
        >
          <option value="unchanged">
            Current (
            {tmdbPreview?.certification ||
              (single ? first.certification : "varies") ||
              "Not rated"}
            )
          </option>
          {Array.from(
            new Set([
              ...(view === "movies" ? certs.movie : certs.tv),
              ...(tmdbPreview?.certification
                ? [tmdbPreview.certification]
                : []),
            ]),
          ).map((cert) => (
            <option key={cert} value={cert}>
              {cert}
            </option>
          ))}
        </select>
      </div>
      <div className="admin-field-row">
        <label>Genres</label>
        <MultiSelectDropdown
          label="Genres"
          options={genres.map((genre) => ({
            value: genre.id,
            label: genre.label,
          }))}
          selected={genreIds}
          onChange={(values) => {
            setGenreIds(values);
            touch("genres");
          }}
          inlinePanel
          onOpenChange={(open) => {
            setGenresOpen(open);
            if (open) {
              requestAnimationFrame(() => {
                actionsRef.current?.scrollIntoView({
                  block: "end",
                  behavior: "smooth",
                });
              });
            }
          }}
          placeholder="No genres selected"
        />
      </div>
      {genresOpen && (
        <div className="library-edit-actions-spacer" aria-hidden="true" />
      )}
      {error && (
        <p className="profile-field-error" role="alert">
          {error}
        </p>
      )}
      {reviewItems.length > 0 && (
        <p className="library-review-notice" role="note">
          <strong>Why this needs review: </strong>
          {single
            ? view === "shows"
              ? "Automatic metadata has not confirmed an intended audience for one or more episodes of this show. "
              : "Automatic metadata has not confirmed an intended audience for this movie. "
            : `${reviewItems.length} selected ${view === "shows" ? "shows contain episodes without" : "movies lack"} a confirmed intended audience. `}
          Content ratings and genres alone may not establish suitability for kids
          or teens. Affected content stays outside restricted default channels
          until reviewed. Check the content and metadata, then choose an explicit
          Intended audience to record your decision.
        </p>
      )}
      <div
        className="admin-table-actions library-edit-actions"
        ref={actionsRef}
      >
        <button
          className="admin-btn"
          disabled={saving}
          onClick={() => void save()}
        >
          {saving ? "Saving…" : "Save Changes"}
        </button>
        <button
          className="admin-btn admin-btn-secondary"
          disabled={saving}
          onClick={onClose}
        >
          Cancel
        </button>
      </div>
    </InfoModal>
  );
}
