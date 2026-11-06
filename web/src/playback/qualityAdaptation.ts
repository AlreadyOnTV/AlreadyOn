// Auto-only hysteresis lowers quality after sustained buffer or decode pressure.
export interface PlaybackPressure {
  nowMs: number;
  bufferedSeconds: number;
  outputHeight: number;
  droppedVideoFrames?: number;
  totalVideoFrames?: number;
}

const QUALITY_HEIGHTS = [1080, 720, 480, 360];
const SAMPLE_WINDOW_MS = 10_000;
const COOLDOWN_MS = 45_000;
const MIN_BUFFER_SECONDS = 1.5;
const MAX_DROPPED_RATIO = 0.05;

export class AutoQualityAdapter {
  private lastSample?: PlaybackPressure;
  private strugglingWindows = 0;
  private cooldownUntil = 0;

  reset() {
    this.lastSample = undefined;
    this.strugglingWindows = 0;
    this.cooldownUntil = 0;
  }

  observe(sample: PlaybackPressure, enabled: boolean): number | null {
    if (!enabled || !Number.isFinite(sample.bufferedSeconds) || sample.bufferedSeconds < 0) {
      this.lastSample = undefined;
      this.strugglingWindows = 0;
      return null;
    }
    if (!this.lastSample) {
      this.lastSample = sample;
      return null;
    }
    if (sample.nowMs - this.lastSample.nowMs < SAMPLE_WINDOW_MS) return null;

    const previous = this.lastSample;
    const hasFrameDelta =
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
    const struggling = sample.bufferedSeconds < MIN_BUFFER_SECONDS || droppedRatio > MAX_DROPPED_RATIO;
    this.strugglingWindows = struggling ? this.strugglingWindows + 1 : 0;
    this.lastSample = sample;

    if (this.strugglingWindows < 2 || sample.nowMs < this.cooldownUntil) return null;
    const lowerHeight = QUALITY_HEIGHTS.find((height) => height < sample.outputHeight);
    if (!lowerHeight) return null;
    this.cooldownUntil = sample.nowMs + COOLDOWN_MS;
    this.strugglingWindows = 0;
    return lowerHeight;
  }
}
