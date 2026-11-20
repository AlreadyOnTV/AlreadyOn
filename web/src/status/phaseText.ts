// Startup benchmark readiness and library progress use distinct, readable labels.
import type { StartupStatus } from '../api/client';

const PHASE_LABELS: Record<string, string> = {
  pending: 'Getting ready',
  benchmarking: 'Measuring server performance',
  scanning: 'Scanning your media folders',
  classifying: 'Looking up show and movie details',
  populating: 'Building your channels',
  scheduling: 'Building the schedule',
};

export function phaseLabel(status: StartupStatus): string {
  return PHASE_LABELS[status.phase] ?? 'Working';
}

export function progressCounts(status: StartupStatus): string {
  const lib = status.library;
  switch (status.phase) {
    case 'scanning':
      return `${lib.filesFound} files found so far`;
    case 'classifying':
      return `${lib.itemsClassified} of ${lib.itemsIdentified} titles looked up`;
    case 'populating':
    case 'scheduling':
      return `${lib.itemsIdentified} titles recognized, ${lib.channelsWithContent} channels stocked so far`;
    default:
      return '';
  }
}
