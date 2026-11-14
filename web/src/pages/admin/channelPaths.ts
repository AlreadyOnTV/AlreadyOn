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
