import type { ChannelFilter } from '../../api/client';

// Materialize legacy exclusions exactly as the channel editor's checkboxes do.
// An explicit empty inclusion list means no paths, rather than all paths.
export function selectedChannelPaths(filter: ChannelFilter, paths: string[]): string[] {
  return filter.includedPaths ?? paths.filter((path) =>
    !(filter.excludedPaths ?? []).some((root) => path === root.replace(/\/$/, '') || path.startsWith(root.replace(/\/$/, '') + '/')),
  );
}

export function setChannelPath(filter: ChannelFilter, paths: string[], path: string, enabled: boolean): ChannelFilter {
  const selected = selectedChannelPaths(filter, paths);
  return {
    ...filter,
    includedPaths: enabled ? [...new Set([...selected, path])] : selected.filter((p) => p !== path),
    excludedPaths: [],
  };
}

export type LibraryPathKind = 'movies' | 'tv' | 'commercials';
export type ChannelPathGroup = 'movies' | 'tv' | 'custom';
export type ChannelPathChoices = Record<ChannelPathGroup, boolean>;

export function defaultPathChoices(kind: LibraryPathKind): ChannelPathChoices {
  return { movies: kind !== 'tv', tv: kind !== 'movies', custom: false };
}

// Custom channels form their own group regardless of their media filter.
export function channelPathGroup(channel: { number: number; filter: ChannelFilter | null }): ChannelPathGroup | null {
  if (channel.number < 100 || channel.number > 199) return 'custom';
  if (!channel.filter) return null;
  return channel.filter.mediaType === 'movie' ? 'movies' : 'tv';
}
