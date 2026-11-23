// Auto playback uses asymmetric hysteresis to recover faster than it upgrades.
export interface PlaybackPressure {
  nowMs: number;
  bufferedSeconds: number;
  outputHeight: number;
  maxHeight?: number;
  serverPressure?: boolean;
  serverCeiling?: number;
  minHeight?: number;
  droppedVideoFrames?: number;
  totalVideoFrames?: number;
}

const QUALITY_HEIGHTS = [240, 360, 480, 720, 1080, 2160];
const SAMPLE_WINDOW_MS = 4_500;

// The encoder fits the source inside a 16:9 quality box. A wide film can
// fill the box horizontally while its actual height remains below the rung.
export function qualityRungHeight(width: number, height: number): number {
  const fittedHeight = Math.max(height, Math.round(width * 9 / 16));
  return QUALITY_HEIGHTS.find(rung => Math.abs(rung - fittedHeight) <= 2) ?? fittedHeight;
}

export class AutoQualityAdapter {
  private lastSample?: PlaybackPressure;
  private strugglingWindows = 0;
  private healthyWindows = 0;

  reset() {
    this.lastSample = undefined;
    this.strugglingWindows = 0;
    this.healthyWindows = 0;
  }

  observe(sample: PlaybackPressure, enabled: boolean): number | null {
    if (!enabled || !Number.isFinite(sample.bufferedSeconds) || sample.bufferedSeconds < 0) {
      this.reset();
      return null;
    }
    const previous = this.lastSample;
    if (previous && sample.nowMs - previous.nowMs < SAMPLE_WINDOW_MS) return null;
    const hasFrameDelta =
      previous !== undefined &&
      sample.droppedVideoFrames !== undefined &&
      sample.totalVideoFrames !== undefined &&
      previous.droppedVideoFrames !== undefined &&
      previous.totalVideoFrames !== undefined &&
      sample.droppedVideoFrames >= previous.droppedVideoFrames &&
      sample.totalVideoFrames >= previous.totalVideoFrames;
    const frameCount = hasFrameDelta
      ? sample.droppedVideoFrames! - previous.droppedVideoFrames! +
        sample.totalVideoFrames! - previous.totalVideoFrames!
      : 0;
    const droppedRatio = hasFrameDelta && frameCount > 0
      ? (sample.droppedVideoFrames! - previous.droppedVideoFrames!) / frameCount
      : 0;
    const draining = previous !== undefined && previous.bufferedSeconds - sample.bufferedSeconds > 2; // A normal HLS segment can change the sampled buffer by up to two seconds.
    const minimum = sample.minHeight ?? 360;
    const serverCeiling = sample.serverCeiling ? Math.max(minimum, sample.serverCeiling) : Infinity;
    const serverPressure = sample.serverCeiling ? sample.outputHeight > serverCeiling : sample.serverPressure;
    const struggling = (serverPressure && sample.outputHeight > minimum) || sample.bufferedSeconds < 3 || (sample.bufferedSeconds < 5 && draining) || droppedRatio > 0.05;
    const healthy = !serverPressure && sample.bufferedSeconds >= 3 && !draining && droppedRatio <= 0.05;
    this.strugglingWindows = struggling ? this.strugglingWindows + 1 : 0;
    this.healthyWindows = healthy ? this.healthyWindows + 1 : 0;
    this.lastSample = sample;

    if (this.strugglingWindows >= 2) {
      this.strugglingWindows = 0;
      return [...QUALITY_HEIGHTS].reverse().find(height => height < sample.outputHeight && height >= minimum) ?? null;
    }
    if (this.healthyWindows >= 3) {
      this.healthyWindows = 0;
      const maximum = Math.min(sample.maxHeight ?? 1080, serverCeiling);
      return QUALITY_HEIGHTS.find(height => height > sample.outputHeight && height <= maximum) ?? null;
    }
    return null;
  }
}
