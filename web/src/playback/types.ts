import type { GuideProgram } from '../api/client';
export interface VideoCapability { container?: string; colorTransfer?: string; hdrMetadata?: string; dolbyVisionProfile?: number; dolbyVisionLevel?: number; codecTag?: string; codec: string; profile: string; level: number; width: number; height: number; fps: number; bitrate: number; pixelFormat: string }
export interface AudioCapability { hls?: boolean; container?: string; profile?: string; spatial?: boolean; codec: string; channels: number; sampleRate: number; bitrate: number }
export interface Capabilities { video: VideoCapability[]; audio: AudioCapability[]; containers: string[]; hls: boolean; mbps: number; autoMaxHeight?: number }
export interface Snapshot { version: number; browser: string; capturedAt: number; capabilities: Capabilities; network?: { origin: string; capturedAt: number; mbps: number; latencyMs?: number } }
// Authorized tune metadata and delivery plan; overlay fields may be absent on older servers.
export interface PlaybackSession {
  channelNumber?: number; channelName?: string; program?: GuideProgram;
  id: string; channelId: number; token: string; expiresAt: string; url: string; ready: boolean;
  plan: { audioTrackIndex?: number; mode: 'direct' | 'remux' | 'transcode'; reason: string; videoCopy: boolean; audioCopy: boolean; lowHeight?: number };
  serverTime: string; entryStartsAt: string; entryEndsAt: string; sourceOffsetSeconds: number; streamOffsetSeconds: number;
  hlsOrigin: string; mediaFileId: number; kind: string; presentationDelaySeconds?: number;
  mediaUrl: string; durationSeconds: number; sourceHeight: number; uhd?: boolean; subtitles: SubtitleTrack[];
  audioTracks?: AudioTrack[];
  details?: StreamDetails;
  runtime?: { cpuUtil?: number };
}
export interface VideoDetails { codec: string; profile?: string; width: number; height: number; fps: number; bitrate: number; pixelFormat?: string; dynamicRange: string; method: string }
export interface AudioDetails { codec: string; profile?: string; channels: number; layout?: string; sampleRate: number; bitrate: number; language?: string; method: string }
export interface StreamDetails { fileName: string; container: string; sourceVideo: VideoDetails; sourceAudio?: AudioDetails; outputVideo: VideoDetails; outputAudio?: AudioDetails; videoReason: string; audioReason?: string; pipeline: PipelineDetails }
export interface PipelineDetails { decoder: string; scaler: string; toneMapper?: string; encoder?: string }
export interface AudioTrack { index: number; language?: string; title?: string; codec: string; channels: number; default?: boolean }
export interface SubtitleTrack { index: number; language?: string; title?: string; codec: string; default?: boolean; forced?: boolean }
