#!/bin/sh
# Immutable, synthetic media for runtime calibration. This recipe is cached
# independently of application source and never reads an installation's media.
set -eu
output=${1:?Usage: generate-benchmark-inputs.sh OUTPUT_DIRECTORY}
mkdir -p "$output"
recipe=$(sha256sum "$0" | cut -d' ' -f1)
if [ -f "$output/recipe.sha256" ] && [ "$(cat "$output/recipe.sha256")" = "$recipe" ] &&
   (cd "$output" && sha256sum -c SHA256SUMS >/dev/null 2>&1); then
    echo 'Benchmark inputs already verified'; exit 0
fi
work_dir=$(mktemp -d "$output/.build.XXXXXXXX")
cleanup() {
    for file in "$work_dir"/*; do [ ! -f "$file" ] || unlink "$file"; done
    rmdir "$work_dir"
}
trap cleanup EXIT INT TERM
started=$(date +%s)
generate() {
    name=$1 width=$2 height=$3 fps=$4 codec=$5 profile=$6 transfer=$7
    filter="noise=alls=3:allf=t:all_seed=1"
    if [ "$codec" = libx265 ]; then pixel=yuv420p10le; crf=28; else pixel=yuv420p; crf=23; fi
    if [ "$profile" = main ]; then pixel=yuv420p; fi
    color_args=''
    if [ "$transfer" != none ]; then
        filter="setparams=color_primaries=bt2020:color_trc=$transfer:colorspace=bt2020nc:range=tv,$filter"
    fi
    # A fixed seed and small encoder thread pool bound build cost on CI runners.
    if [ "$codec" = libx265 ]; then
        params='pools=2:frame-threads=2:log-level=error'
        # QSV VPP only enables HDR tone mapping when static HDR side data exists.
        # Transfer tags alone can otherwise produce a misleading successful no-op.
        if [ "$transfer" = smpte2084 ]; then
            params="$params:master-display=G(8500,39850)B(6550,2300)R(35400,14600)WP(15635,16450)L(10000000,1):max-cll=1000,400"
        fi
        set -- -x265-params "$params"
    else set -- -threads 2; fi
    ffmpeg -hide_banner -v error -nostdin -y -f lavfi \
        -i "testsrc2=size=${width}x${height}:rate=$fps:duration=3,$filter,format=$pixel" \
        -f lavfi -i 'anullsrc=channel_layout=stereo:sample_rate=48000:duration=3' \
        -map 0:v:0 -map 1:a:0 -shortest -c:v "$codec" -preset ultrafast \
        -profile:v "$profile" -crf "$crf" "$@" -c:a aac -b:a 192k \
        "$work_dir/$name.mkv"
}
for fps in 24 30 60; do
    generate "hevc-main10-4k-pq-$fps" 3840 2160 "$fps" libx265 main10 smpte2084
    generate "hevc-main10-4k-hlg-$fps" 3840 2160 "$fps" libx265 main10 arib-std-b67
done
generate hevc-sdr-1080p 1920 1080 30 libx265 main none
generate h264-sdr-1080p 1920 1080 60 libx264 high none
generate h264-sdr-480p 854 480 30 libx264 main none
(cd "$work_dir" && sha256sum ./*.mkv > SHA256SUMS)
printf '%s\n' "$recipe" > "$work_dir/recipe.sha256"
for file in "$work_dir"/*; do chmod 0644 "$file"; mv "$file" "$output/"; done
echo "Benchmark inputs: $(du -sk "$output" | cut -f1) KiB, $(($(date +%s)-started)) seconds"
