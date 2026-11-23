#!/bin/sh
# Build-time availability and real software smoke test, no GPU required.
set -eu
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
ffmpeg -hide_banner -encoders > "$work/encoders" 2>&1
ffmpeg -hide_banner -decoders > "$work/decoders" 2>&1
ffmpeg -hide_banner -filters > "$work/filters" 2>&1
for name in libopenh264 libx264 libx265 aac h264_qsv h264_vaapi h264_nvenc; do
    grep -qw "$name" "$work/encoders" || { echo "Missing encoder: $name" >&2; exit 1; }
done
for name in h264 hevc av1 mpeg2video vc1 aac ac3 eac3 truehd dca flac opus; do
    grep -qw "$name" "$work/decoders" || { echo "Missing decoder: $name" >&2; exit 1; }
done
for name in zscale tonemap loudnorm volume alimiter scale_vaapi tonemap_vaapi vpp_qsv scale_cuda tonemap_opencl; do
    grep -qw "$name" "$work/filters" || { echo "Missing filter: $name" >&2; exit 1; }
done
ffmpeg -v error -f lavfi -i testsrc2=size=64x64:rate=10 -t 5 -c:v libopenh264 "$work/burst.mp4"
# A 4s head start on a 5s file finishes in about a second; paced from zero it takes 5s.
burst_start=$(date +%s%N)
ffmpeg -v error -readrate 1 -readrate_initial_burst 4 -i "$work/burst.mp4" -f null -
if [ $(( ($(date +%s%N) - burst_start) / 1000000 )) -gt 3000 ]; then echo 'FFmpeg ignores -readrate_initial_burst' >&2; exit 1; fi
command -v nice
command -v ionice
ffmpeg -v error -f lavfi -i testsrc2=size=128x72:rate=24 -f lavfi -i sine=frequency=440:sample_rate=48000 \
    -t 1 -c:v libopenh264 -profile:v constrained_baseline -threads 1 \
    -af loudnorm=I=-16:TP=-1.5:LRA=50 -c:a aac -ac 2 -ar 48000 -f mpegts "$work/test.ts"
ffmpeg -v error -i "$work/test.ts" -f null -
ffprobe -v error -show_entries stream=codec_name -of csv=p=0 "$work/test.ts"
ffmpeg -v error -f lavfi -i testsrc2=size=128x72:rate=24 -t 0.25 \
    -vf format=yuv420p10le -c:v libx265 -preset ultrafast -x265-params pools=1:frame-threads=1:log-level=error \
    -color_primaries bt2020 -color_trc smpte2084 -colorspace bt2020nc "$work/hdr.mkv"
ffmpeg -v error -i "$work/hdr.mkv" \
    -vf 'setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc,zscale=tin=smpte2084:min=bt2020nc:pin=bt2020:t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p' \
    -c:v libx264 -preset veryfast -tune zerolatency -threads 1 -f null -
echo 'Media stack checks passed (GPU execution requires a compatible host).'
