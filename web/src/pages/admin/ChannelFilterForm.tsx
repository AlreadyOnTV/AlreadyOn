import SettingToggle from './SettingToggle';
// The "what does this channel air" form for any channel: audience,
// genres, media type, era and a title filter, with a live match count.
import { useEffect, useState } from 'react';
import { getGenres, getFilterOptions, getFolderSettings, type FilterCatalog, type ChannelFilter, type Genre } from '../../api/client';
import MultiSelectDropdown from './MultiSelectDropdown';
import PhraseInput from './PhraseInput';

export default function ChannelFilterForm({ filter, onChange }: { filter: ChannelFilter; onChange: (f: ChannelFilter) => void }) {
  const [catalog, setCatalog] = useState<FilterCatalog>({
    age: [],
    era: [],
  });
  const [paths, setPaths] = useState<string[]>([]);
  const [genres, setGenres] = useState<Genre[]>([]);
  const [genreError, setGenreError] = useState<string | null>(null);

  useEffect(() => {
    Promise.all([getFilterOptions(), getFolderSettings()])
      .then(([c, f]) => {
        setCatalog(c);
        setPaths([...new Set([...f.tv.paths.map((p) => p.path), ...f.movies.paths.map((p) => p.path)])]);
      })
      .catch((e: Error) => setGenreError(e.message));
    getGenres()
      .then(setGenres)
      .catch((err: Error) => setGenreError(err.message));
  }, []);

  const set = (patch: Partial<ChannelFilter>) => onChange({ ...filter, ...patch });

  return (
    <div className="channel-filter-form">
      <div className="admin-field-row">
        <label htmlFor="channel-age">Age Restrictions</label>
        <select id="channel-age" value={filter.ageRestrictionId ?? filter.audienceTier} onChange={(e) => set({ ageRestrictionId: e.target.value })}>
          {catalog.age.map((o) => (
            <option key={o.id} value={o.id}>
              {o.name}
            </option>
          ))}
        </select>
      </div>
      <div className="admin-field-row">
        <label>Genres</label>
        <MultiSelectDropdown
          label="Genres"
          placeholder="Any Genre"
          options={genres.map((g) => ({ value: g.id, label: g.label }))}
          selected={filter.genres}
          onChange={(genres) => set({ genres })}
        />
      </div>
      <div className="admin-field-row">
        <label>Excluded Genres</label>
        <MultiSelectDropdown label="Excluded Genres" placeholder="None"
          options={genres.map((g) => ({ value: g.id, label: g.label }))}
          selected={filter.excludedGenres ?? []} onChange={(excludedGenres) => set({ excludedGenres })} />
      </div>
      <div className="admin-field-row">
        <label>Require Known Premiere Year</label>
        <SettingToggle label="Require Known Premiere Year" checked={filter.requireKnownYear ?? false}
          onChange={(requireKnownYear) => set({ requireKnownYear })} />
      </div>
      <div className="admin-field-row">
        <label>Shows Or Movies</label>
        <select value={filter.mediaType} onChange={(e) => set({ mediaType: e.target.value as ChannelFilter['mediaType'] })}>
          <option value="both">Both</option>
          <option value="tv">TV Shows</option>
          <option value="movie">Movies</option>
        </select>
      </div>
      <div className="admin-field-row">
        <label>Date Range</label>
        <MultiSelectDropdown
          label="Date Range"
          placeholder="All"
          options={catalog.era.map((o) => ({ value: o.id, label: o.name }))}
          selected={filter.eras ?? [filter.era]}
          onChange={(eras) => set({ eras, era: 'both' })}
        />
      </div>
      <div className="admin-field-row">
        <label>Title Contains</label>
        <PhraseInput
          values={filter.titlePhrases ?? (filter.titleContains ? [filter.titleContains] : [])}
          onChange={(titlePhrases) => set({ titlePhrases, titleContains: '' })}
        />
      </div>
      <div className="admin-field-row">
        <label>Included Media Paths</label>
        <div className="included-paths">
          {[...new Set([...paths, ...(filter.includedPaths ?? []), ...(filter.excludedPaths ?? [])])].sort().map((path) => {
            const selected =
              filter.includedPaths ??
              paths.filter((p) => !(filter.excludedPaths ?? []).some((root) => p === root || p.startsWith(root.replace(/\/$/, '') + '/')));
            return (
              <label key={path}>
                <SettingToggle
                  label={path}
                  checked={selected.includes(path)}
                  onChange={(enabled) => set({ includedPaths: enabled ? [...selected, path] : selected.filter((p) => p !== path), excludedPaths: [] })}
                />
                <span>{path}</span>
              </label>
            );
          })}
        </div>
      </div>
      {genreError && <p className="profile-field-error">{genreError}</p>}
    </div>
  );
}
