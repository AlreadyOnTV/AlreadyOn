#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
repo_root="$PWD"
playback_tmp="$(mktemp -d /tmp/alreadyon-playback-e2e.XXXXXXXX)"
server_pid=''
cleanup() {
  result=$?
  if [[ -n "$server_pid" ]]; then kill -TERM "$server_pid" 2>/dev/null || true; wait "$server_pid" || true; fi
  if [[ $result -ne 0 && ${PLAYBACK_KEEP_FAILED:-0} == 1 ]]; then
    echo "Retained synthetic fixture: $playback_tmp" >&2
  else
    rm -rf "$playback_tmp"
  fi
}
trap cleanup EXIT INT TERM
chmod 700 "$playback_tmp"
touch "$playback_tmp/.synthetic-test"
mkdir "$playback_tmp/media"
# Use one ephemeral application listener and a compiled-in disposable data directory.
read -r management_port < <(python3 - <<'PY'
import socket
with socket.socket() as a:
 a.bind(('127.0.0.1',0))
 print(a.getsockname()[1])
PY
)
(cd web && npm run build)
(cd server && go build -tags=e2e -ldflags "-X github.com/alreadyonapp/alreadyon/server/internal/config.dataDir=$playback_tmp/data -X github.com/alreadyonapp/alreadyon/server/internal/config.webDir=$repo_root/web/dist" -o "$playback_tmp/server" ./cmd/server)
# Two top-left blocks encode the source second (base 12, 18 luma units per digit)
# so the browser test can check decoded content independently of its diagnostics.
clock="if(lt(X,20),20+18*floor(floor(T+0.0001)/12),20+18*mod(floor(T+0.0001),12))"
# Two subtitle tracks (English, German) let the controls test pick a language.
printf '1\n00:00:15,000 --> 00:02:00,000\nE2E SUBTITLE ONE\n' > "$playback_tmp/en.srt"
printf '1\n00:00:15,000 --> 00:02:00,000\nE2E UNTERTITEL\n' > "$playback_tmp/de.srt"
ffmpeg -nostdin -v error -f lavfi -i 'testsrc2=duration=120:size=320x240:rate=30' -f lavfi -i 'sine=duration=120:sample_rate=48000' -i "$playback_tmp/en.srt" -i "$playback_tmp/de.srt" -filter_complex "[0:v]format=yuv420p[bg];nullsrc=s=40x20:r=30:d=120,format=yuv420p,geq=lum='$clock':cb=128:cr=128[clock];[bg][clock]overlay=0:0:format=yuv420[v]" -map '[v]' -map 1:a -map 2:0 -map 3:0 -map 3:0 -map 3:0 -map 3:0 -map 3:0 -c:s mov_text -metadata:s:s:0 language=eng -metadata:s:s:1 language=deu -metadata:s:s:2 language=fra -metadata:s:s:3 language=spa -metadata:s:s:4 language=ita -metadata:s:s:5 language=por -c:v libopenh264 -b:v 600k -g 60 -c:a aac -b:a 192k -ac 2 -shortest "$playback_tmp/media/program.mp4"
ffmpeg -nostdin -v error -f lavfi -i 'testsrc2=duration=60:size=1280x720:rate=30' -f lavfi -i 'sine=duration=60:sample_rate=48000' -c:v libopenh264 -b:v 800k -g 60 -c:a aac -b:a 128k -ac 2 -shortest "$playback_tmp/media/hd.mp4"
# A separate multi-audio fixture keeps existing direct-play tests single-track.
ffmpeg -nostdin -v error -i "$playback_tmp/media/program.mp4" -f lavfi -i 'sine=frequency=880:duration=120:sample_rate=48000' -map 0:v -map 0:a -map 1:a -map 0:a -c:v copy -c:a aac -b:a 128k -ac 2 -metadata:s:a:0 language=jpn -metadata:s:a:1 language=eng -metadata:s:a:2 language=eng -metadata:s:a:2 title=Commentary -disposition:a:0 default -disposition:a:1 0 -disposition:a:2 0 "$playback_tmp/media/multi.mkv"
for color in blue red green; do
  suffix="-$color"; [[ $color == blue ]] && suffix=''
  ffmpeg -nostdin -v error -f lavfi -i "color=$color:size=640x360:rate=24:duration=12" -c:v mpeg4 "$playback_tmp/media/ad$suffix.avi"
done
ALREADYON_WEB_PORT="$management_port" "$playback_tmp/server" > "$playback_tmp/server.log" 2>&1 &
server_pid=$!
export PLAYBACK_TEST_ROOT="$playback_tmp" PLAYBACK_TEST_URL="http://localhost:$management_port" PLAYBACK_TEST_SERVER_PID="$server_pid"
node tests/e2e/playback.cjs

# Full delivery runs also cover the focused UI suites with isolated fixtures.
if [[ -z ${PLAYBACK_TEST_ONLY:-} ]]; then
  node tests/e2e/channel-filters.cjs
  tests/e2e/library-catalog.sh
  node tests/e2e/profile-autosave.cjs "$PLAYBACK_TEST_URL"
  node tests/e2e/settings-feedback.cjs "$PLAYBACK_TEST_URL"
fi
