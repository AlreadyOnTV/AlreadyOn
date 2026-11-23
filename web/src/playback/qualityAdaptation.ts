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

export interface AutoDecision {
  reason: "healthy" | "server_ceiling" | "low_buffer" | "draining_buffer" | "dropped_frames" | "buffering";
  bufferMs: number;
  bufferDropMs: number;
  droppedFrames: number;
  totalFrames?: number;
  serverCeiling: number;
}

const QUALITY_HEIGHTS = [240, 360, 480, 720, 1080, 2160];
const SAMPLE_WINDOW_MS = 4_500;
const HEALTHY_WINDOWS_FOR_UPGRADE = 6;
const HEALTHY_WINDOWS_AFTER_DOWNSHIFT = 12;

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
  private recovering = false;
  decision?: AutoDecision;

  reset() {
    this.lastSample = undefined;
    this.decision = undefined;
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
    const reason = serverPressure && sample.outputHeight > minimum ? "server_ceiling"
      : sample.bufferedSeconds < 2 ? "low_buffer"
      : sample.bufferedSeconds < 3 && draining ? "draining_buffer"
      : droppedRatio > 0.05 ? "dropped_frames" : undefined;
    const healthy = !serverPressure && sample.bufferedSeconds >= 3 && !draining && droppedRatio <= 0.05;
    this.strugglingWindows = reason ? this.strugglingWindows + 1 : 0;
    this.healthyWindows = healthy ? this.healthyWindows + 1 : 0;
    this.lastSample = sample;
    const recordDecision = (cause: AutoDecision["reason"]) => {
      this.decision = {
        reason: cause,
        bufferMs: Math.round(sample.bufferedSeconds * 1000),
        bufferDropMs: Math.round(((previous?.bufferedSeconds ?? sample.bufferedSeconds) - sample.bufferedSeconds) * 1000),
        droppedFrames: hasFrameDelta ? sample.droppedVideoFrames! - previous.droppedVideoFrames! : 0,
        totalFrames: frameCount,
        serverCeiling: sample.serverCeiling ?? 0,
      };
    };

    if (reason && this.strugglingWindows >= 2) {
      this.strugglingWindows = 0;
      const next = [...QUALITY_HEIGHTS].reverse().find(height => height < sample.outputHeight && height >= minimum);
      if (next !== undefined) {
        this.recovering = true;
        recordDecision(reason);
      }
      return next ?? null;
    }
    const required = this.recovering ? HEALTHY_WINDOWS_AFTER_DOWNSHIFT : HEALTHY_WINDOWS_FOR_UPGRADE;
    if (this.healthyWindows >= required) {
      const maximum = Math.min(sample.maxHeight ?? 1080, serverCeiling);
      const next = QUALITY_HEIGHTS.find(height => height > sample.outputHeight && height <= maximum);
      if (next !== undefined) {
        this.healthyWindows = 0;
        this.recovering = false;
        recordDecision("healthy");
      }
      return next ?? null;
    }
    return null;
  }
}
