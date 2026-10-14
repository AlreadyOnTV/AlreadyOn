// Preserve visible guide order and skip empty rows. A lone playable channel
// stays tuned rather than restarting playback when the viewer keeps surfing.
export function circularChannelIndex(channels: { programs: unknown[] }[], current: number, direction: number): number | null {
  if (!channels.length || !direction) return null;
  const step = direction > 0 ? 1 : -1;
  const start = current >= 0 && current < channels.length ? current : step > 0 ? -1 : 0;
  for (let offset = 1; offset <= channels.length; offset++) {
    const index = ((start + step * offset) % channels.length + channels.length) % channels.length;
    if (channels[index].programs.length) return index === current ? null : index;
  }
  return null;
}
