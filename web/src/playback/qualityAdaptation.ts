// Auto playback uses asymmetric hysteresis to recover faster than it upgrades.
export interface PlaybackPressure {
  nowMs: number;
  bufferedSeconds: number;
  outputHeight: number;
  maxHeight?: number;
  serverPressure?: boolean;
  droppedVideoFrames?: number;
  totalVideoFrames?: number;
}

const QUALITY_HEIGHTS = [240, 360, 480, 720, 1080, 2160];
const SAMPLE_WINDOW_MS = 4_500;

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
    const draining = previous !== undefined && previous.bufferedSeconds - sample.bufferedSeconds > 0.5;
    const struggling = (sample.serverPressure && sample.outputHeight > 360) || sample.bufferedSeconds < 3 || (sample.bufferedSeconds < 5 && draining) || droppedRatio > 0.05;
    const healthy = !sample.serverPressure && sample.bufferedSeconds >= 4.5 && !draining && droppedRatio <= 0.05;
    this.strugglingWindows = struggling ? this.strugglingWindows + 1 : 0;
    this.healthyWindows = healthy ? this.healthyWindows + 1 : 0;
    this.lastSample = sample;

    if (this.strugglingWindows >= 2) {
      this.strugglingWindows = 0;
      return [...QUALITY_HEIGHTS].reverse().find(height => height < sample.outputHeight) ?? null;
    }
    if (this.healthyWindows >= 3) {
      this.healthyWindows = 0;
      const maximum = sample.maxHeight ?? 1080;
      return QUALITY_HEIGHTS.find(height => height > sample.outputHeight && height <= maximum) ?? null;
    }
    return null;
  }
}
