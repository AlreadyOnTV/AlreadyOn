import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  getCatalog,
  getCertifications,
  getGenres,
  type CatalogItem,
  type CatalogView,
  type Genre,
} from "../../api/client";
import EditItemsDialog from "./EditItemsDialog";
import MultiSelectDropdown from "./MultiSelectDropdown";

const PAGE_SIZE_OPTIONS = [10, 25, 50, 100];
const EMPTY_CATALOG_FILTERS = {
  search: "",
  match: "",
  audience: "",
  genre: "",
  rating: "",
};
type CatalogFilters = typeof EMPTY_CATALOG_FILTERS;

const catalogTitle = (item: CatalogItem) => {
  const title = item.title || item.path.split("/").pop() || "Untitled";
  // Identified shows may already carry their release year in the title.
  const suffix = item.year ? ` (${item.year})` : "";
  return suffix && title.endsWith(suffix) ? title.slice(0, -suffix.length) : title;
};

type LibraryView = Extract<CatalogView, "movies" | "shows">;

function LibraryFilters({
  view,
  filters,
  onChange,
}: {
  view: LibraryView;
  filters: CatalogFilters;
  onChange: (filters: CatalogFilters) => void;
}) {
  const genreFilterRef = useRef<HTMLDivElement>(null);
  const [genres, setGenres] = useState<Genre[]>([]);
  const [ratings, setRatings] = useState<{ movie: string[]; tv: string[] }>({
    movie: [],
    tv: [],
  });

  useEffect(() => {
    getGenres()
      .then(setGenres)
      .catch(() => undefined);
    getCertifications()
      .then(setRatings)
      .catch(() => undefined);
  }, []);

  const changeFilter = (key: keyof CatalogFilters, value: string) => {
    onChange({ ...filters, [key]: value });
  };

  return (
    <div className="library-filters">
      <label>
        Search
        <input
          type="search"
          placeholder="Title or file path"
          value={filters.search}
          onChange={(event) => changeFilter("search", event.target.value)}
        />
      </label>
      <label>
        Channel Status
        <select
          value={filters.match}
          onChange={(event) => changeFilter("match", event.target.value)}
        >
          <option value="">All Statuses</option>
          <option value="matched">Matched</option>
          <option value="unmatched">No Matches</option>
          <option value="review">Needs Review</option>
        </select>
      </label>
      <label>
        Audience
        <select
          value={filters.audience}
          onChange={(event) => changeFilter("audience", event.target.value)}
        >
          <option value="">All audiences</option>
          <option value="young_child">Kids</option>
          <option value="teen">Teen</option>
          <option value="adult">Unrestricted</option>
        </select>
      </label>
      <label>
        Content Rating
        <select
          value={filters.rating}
          onChange={(event) => changeFilter("rating", event.target.value)}
        >
          <option value="">All ratings</option>
          {(view === "movies" ? ratings.movie : ratings.tv).map((rating) => (
            <option key={rating} value={rating}>
              {rating}
            </option>
          ))}
        </select>
      </label>
      <label>
        Genre
        <div className="library-genre-filter" ref={genreFilterRef}>
          <MultiSelectDropdown
            inlinePanel
            label="Genres"
            placeholder="All genres"
            options={genres.map((genre) => ({
              value: genre.id,
              label: genre.label,
            }))}
            selected={filters.genre ? filters.genre.split(",") : []}
            onChange={(values) => changeFilter("genre", values.join(","))}
            onOpenChange={(open) => {
              if (!open) return;
              requestAnimationFrame(() => {
                genreFilterRef.current
                  ?.querySelector(".multi-select-panel")
                  ?.scrollIntoView({ block: "nearest", behavior: "smooth" });
              });
            }}
          />
        </div>
      </label>
    </div>
  );
}

export default function LibraryCatalog({ view, active, filterTarget }: { view: LibraryView; active: boolean; filterTarget: HTMLElement | null }) {
  const [filters, setFilters] = useState<CatalogFilters>(EMPTY_CATALOG_FILTERS);
  const [page, setPage] = useState(0);
  const [sort, setSort] = useState("title");
  const [total, setTotal] = useState(0);
  const [items, setItems] = useState<CatalogItem[]>([]);
  const [selected, setSelected] = useState<Map<number, CatalogItem>>(new Map());
  const [editing, setEditing] = useState<CatalogItem[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [revision, setRevision] = useState(0);
  const [pageSize, setPageSize] = useState(10);

  useEffect(() => {
    const controller = new AbortController();
    const timer = setTimeout(() => {
      setLoading(true);
      getCatalog(
        {
          ...filters,
          view,
          sort,
          limit: String(pageSize),
          offset: String(page * pageSize),
        },
        controller.signal,
      )
        .then((result) => {
          if (controller.signal.aborted) return;
          if (!result.items.length && result.total && page) {
            setPage(Math.max(0, Math.ceil(result.total / pageSize) - 1));
            return;
          }
          setItems(result.items);
          setTotal(result.total);
          setError(null);
        })
        .catch((err: Error) => {
          if (!controller.signal.aborted) setError(err.message);
        })
        .finally(() => {
          if (!controller.signal.aborted) setLoading(false);
        });
    }, 180);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [filters, view, page, pageSize, revision, sort]);

  const toggle = (item: CatalogItem) =>
    setSelected((current) => {
      const next = new Map(current);
      if (next.has(item.id)) next.delete(item.id);
      else next.set(item.id, item);
      return next;
    });
  const all = items.length > 0 && items.every((item) => selected.has(item.id));
  const changeFilters = (next: CatalogFilters) => {
    setFilters(next);
    setPage(0);
    setSelected(new Map());
  };
  const toggleSort = (
    column: "title" | "year" | "audience" | "rating" | "channels",
  ) => {
    const pairs = {
      title: ["title", "title-desc"],
      year: ["year-asc", "year"],
      audience: ["audience", "audience-desc"],
      rating: ["rating", "rating-desc"],
      channels: ["channels", "channels-desc"],
    } as const;
    const [ascending, descending] = pairs[column];
    setSort((current) => (current === ascending ? descending : ascending));
    setPage(0);
    setSelected(new Map());
  };
  const sortDirection = (ascending: string, descending: string) =>
    sort === ascending
      ? "ascending"
      : sort === descending
        ? "descending"
        : "none";
  const selectPage = () =>
    setSelected((current) => {
      const next = new Map(current);
      items.forEach((item) => {
        if (all) next.delete(item.id);
        else next.set(item.id, item);
      });
      return next;
    });
  const title = view === "movies" ? "Manage Movies" : "Manage TV Shows";
  const noun = view === "movies" ? "movies" : "shows";

  return (
    <div className="library-section-layout" data-library-section={view}>
      {filterTarget && createPortal(
        <aside className="library-filter-menu" hidden={!active}
          aria-label={`${view === "movies" ? "Movie" : "TV Show"} filters`}>
          <h3>{title} Filters</h3>
          <LibraryFilters view={view} filters={filters} onChange={changeFilters} />
        </aside>, filterTarget,
      )}
      <section
        className="library-catalog"
        aria-labelledby={`${view}-library-title`}
      >
        <header className="library-box-header">
          <div className="library-box-actions">
            <button
              className="admin-btn"
              disabled={!selected.size || loading}
              onClick={() => setEditing([...selected.values()])}
            >
              Edit selected{selected.size ? ` (${selected.size})` : ""}
            </button>
            {selected.size > 0 && (
              <button
                className="admin-btn admin-btn-secondary"
                onClick={() => setSelected(new Map())}
              >
                Clear selection
              </button>
            )}
            <button
              className="admin-btn admin-btn-secondary"
              onClick={() => {
                changeFilters(EMPTY_CATALOG_FILTERS);
                setPage(0);
                setSelected(new Map());
              }}
            >
              Reset filters
            </button>
          </div>
          <h3 className="library-box-title" id={`${view}-library-title`}>
            {title}
          </h3>
          <div className="library-page-controls">
            <select
              aria-label="Items per page"
              value={pageSize}
              onChange={(event) => {
                setPageSize(Number(event.target.value));
                setPage(0);
              }}
            >
              {PAGE_SIZE_OPTIONS.map((size) => (
                <option key={size} value={size}>
                  {size}
                </option>
              ))}
            </select>
            <button
              className="admin-btn admin-btn-secondary"
              disabled={!page}
              onClick={() => setPage((current) => current - 1)}
            >
              Previous
            </button>
            <button
              className="admin-btn admin-btn-secondary"
              disabled={(page + 1) * pageSize >= total}
              onClick={() => setPage((current) => current + 1)}
            >
              Next
            </button>
          </div>
        </header>
        {error && (
          <p className="profile-field-error" role="alert">
            {error}{" "}
            <button
              className="admin-btn"
              onClick={() => setRevision((n) => n + 1)}
            >
              Retry
            </button>
          </p>
        )}
        {notice && (
          <p className="admin-section-hint" role="status">
            {notice}
          </p>
        )}
        {loading ? (
          <p className="admin-section-hint" role="status">
            Loading library…
          </p>
        ) : !error && !items.length ? (
          <p className="admin-section-hint">
            No {noun} match these filters. Newly scanned files appear after
            identification.
          </p>
        ) : (
          !error && (
            <>
              <div className="library-table-scroll">
                <table className="admin-table library-table">
                  <colgroup>
                    <col className="select-column" />
                    <col className="title-column" />
                    <col className="channels-column" />
                  </colgroup>
                  <thead>
                    <tr>
                      <th><input type="checkbox" checked={all} onChange={selectPage}
                        aria-label="Select all rows on this page" /></th>
                      <th aria-sort={sortDirection("title", "title-desc")}>
                        <button className="library-sort-button" onClick={() => toggleSort("title")}>Title</button>
                      </th>
                      <th aria-sort={sortDirection("channels", "channels-desc")}>
                        <button className="library-sort-button" onClick={() => toggleSort("channels")}>Status</button>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {items.map((item) => (
                      <tr key={item.id}>
                        <td><div className="library-row-actions">
                          <input type="checkbox" checked={selected.has(item.id)}
                            onChange={() => toggle(item)} aria-label={`Select ${item.title}`} />
                          <button className="admin-btn admin-btn-secondary library-edit-button"
                            aria-label={`Edit ${item.title}`} onClick={() => setEditing([item])}>
                            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
                              <path d="m16 3 5 5-12 12-6 1 1-6Z M14 5l5 5" />
                            </svg>
                          </button>
                        </div></td>
                        <td className="library-title-cell" title={`${catalogTitle(item)} (${item.year ?? "Year unknown"})`}>
                          <strong>{catalogTitle(item)}</strong> ({item.year ?? "Year unknown"})
                        </td>
                        <td>{item.needsReview ? "Needs Review" : item.matched ? "Matched" : "No Matches"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="library-pager">
                <span className="admin-section-hint">
                  {page * pageSize + 1}–{page * pageSize + items.length} of{" "}
                  {total}
                </span>
                <button
                  className="admin-btn admin-btn-secondary"
                  disabled={!page}
                  onClick={() => setPage((n) => n - 1)}
                >
                  Previous
                </button>
                <button
                  className="admin-btn admin-btn-secondary"
                  disabled={(page + 1) * pageSize >= total}
                  onClick={() => setPage((n) => n + 1)}
                >
                  Next
                </button>
              </div>
            </>
          )
        )}
        {editing && (
          <EditItemsDialog
            items={editing}
            view={view}
            onClose={() => setEditing(null)}
            onSaved={(count) => {
              setEditing(null);
              setSelected(new Map());
              setRevision((n) => n + 1);
              setNotice(
                `Saved ${count} ${view === "shows" ? "show" : "movie"}${count === 1 ? "" : "s"}. Changes will be included in the next scheduled lineup refresh.`,
              );
            }}
          />
        )}
      </section>
    </div>
  );
}
