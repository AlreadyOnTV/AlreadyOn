// Load and save commercial selections separately from the channel editor's rendering.
import { getChannelCommercials, getFolderSettings, updateChannelCommercials, type ChannelCommercialPolicy } from '../../api/client';

export interface ChannelCommercialsDraft {
  enabled: boolean;
  // Every configured commercial folder, and the ones this channel uses.
  available: string[];
  selected: string[];
}

// A new channel defaults to commercials on with every folder selected; an
// existing channel shows its effective (saved or inherited) settings.
export async function loadChannelCommercials(channelId?: number): Promise<ChannelCommercialsDraft> {
  if (channelId === undefined) {
    const folders = await getFolderSettings();
    const available = folders.commercials.paths.map((p) => p.path);
    return { enabled: true, available, selected: available };
  }
  const policy = await getChannelCommercials(channelId);
  return { enabled: policy.effectiveEnabled, available: policy.availableFolders, selected: policy.effectiveFolders.filter((f) => policy.availableFolders.includes(f)) };
}

// Saves only what differs from the global defaults: an enabled value equal to
// the global setting is stored as "inherit", and selecting every folder is
// stored as "all" so folders added later are picked up automatically.
export async function saveChannelCommercials(channelId: number, draft: ChannelCommercialsDraft, current?: ChannelCommercialPolicy): Promise<void> {
  const policy = current ?? (await getChannelCommercials(channelId));
  const allFolders = draft.available.every((f) => draft.selected.includes(f));
  const enabled = draft.enabled === policy.globalEnabled ? null : draft.enabled;
  const folders = allFolders ? [] : draft.selected;
  const explicitFolders = !allFolders;
  const unchanged = enabled === policy.enabled && JSON.stringify(folders) === JSON.stringify(policy.folders) && !policy.foldersFallback && explicitFolders === !!policy.explicitFolders;
  if (!unchanged) await updateChannelCommercials(channelId, { enabled, folders, explicitFolders });
}
