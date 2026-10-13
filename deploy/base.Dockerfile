# Weekly production media base. Application source is deliberately absent:
# PR builds add only the current binaries, web assets and runtime metadata.
ARG UBUNTU_VERSION=26.04

# Compile Ubuntu's security-maintained FFmpeg source with the software encoder
# required by AlreadyOn and all three hardware APIs. No host-specific CPU flags.
FROM ubuntu:${UBUNTU_VERSION} AS ffmpeg-build
ARG FFMPEG_BUILD_JOBS=2
RUN sed -i 's/^Types: deb$/Types: deb deb-src/' /etc/apt/sources.list.d/ubuntu.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends build-essential pkg-config nasm dpkg-dev clang \
    libopenh264-dev libx264-dev libx265-dev libvpl-dev libva-dev libass-dev \
    libopus-dev libvpx-dev libmp3lame-dev libzimg-dev libgnutls28-dev zlib1g-dev libdrm-dev \
    libdav1d-dev libaom-dev libsvtav1enc-dev libvorbis-dev libwebp-dev libsoxr-dev \
    libffmpeg-nvenc-dev \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY deploy/patches/ffmpeg-readrate-initial-burst.patch /patches/
RUN apt-get update && apt-get source ffmpeg \
    && cd ffmpeg-* \
    && patch -p1 < /patches/ffmpeg-readrate-initial-burst.patch \
    && ./configure --prefix=/opt/ffmpeg --cpu=generic --disable-debug --disable-doc --disable-ffplay \
    --disable-autodetect --enable-gpl --enable-libopenh264 --enable-libx264 \
    --enable-libx265 --enable-libvpl --enable-vaapi --enable-libdrm \
    --enable-libass --enable-libopus --enable-libvpx --enable-libmp3lame \
    --enable-libzimg --enable-gnutls --enable-zlib --enable-libdav1d --enable-libaom \
    --enable-libsvtav1 --enable-libvorbis --enable-libwebp --enable-libsoxr \
    --enable-ffnvcodec --enable-nvenc --enable-nvdec --enable-cuvid \
    --enable-cuda-llvm --nvcc=clang \
    && make -j"$FFMPEG_BUILD_JOBS" && make install \
    && strip /opt/ffmpeg/bin/ffmpeg /opt/ffmpeg/bin/ffprobe \
    && mkdir -p /opt/ffmpeg/share/build \
    && cp config.h ffbuild/config.mak COPYING* /opt/ffmpeg/share/build/ \
    && dpkg-query -W > /opt/ffmpeg/share/build/packages.txt \
    && ldd /opt/ffmpeg/bin/ffmpeg /opt/ffmpeg/bin/ffprobe | awk '$3 ~ /^\// {print $3}' | sort -u \
    | xargs -r readlink -f | xargs -r dpkg-query -S | cut -d: -f1 | sort -u \
    > /opt/ffmpeg/share/build/runtime-packages.txt

FROM ubuntu:${UBUNTU_VERSION} AS runtime-rootfs
ENV NVIDIA_DRIVER_CAPABILITIES=compute,video,utility
# This stage contains runtime libraries and dynamically loaded GPU drivers only;
# compilers, package indexes and dependency caches remain in earlier stages.
COPY --from=ffmpeg-build /opt/ffmpeg/share/build/runtime-packages.txt /tmp/runtime-packages.txt
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata util-linux \
    intel-media-va-driver-non-free i965-va-driver-shaders libmfx-gen1.2 libvpl2 \
    mesa-libgallium $(cat /tmp/runtime-packages.txt) \
    && rm -rf /var/lib/apt/lists/* /tmp/runtime-packages.txt
COPY --from=ffmpeg-build /opt/ffmpeg/bin/ffmpeg /opt/ffmpeg/bin/ffprobe /usr/local/bin/
COPY --from=ffmpeg-build /opt/ffmpeg/share/build /usr/local/share/alreadyon/ffmpeg-build
COPY deploy/check-media-stack.sh /usr/local/bin/alreadyon-check-media-stack
RUN chmod 0755 /usr/local/bin/alreadyon-check-media-stack && alreadyon-check-media-stack

# Ubuntu includes Pebble for container orchestration; AlreadyOn owns its entrypoint.
# Flatten the cleaned rootfs so removed upstream files do not remain in image layers.
# Retain package copyright notices and FFmpeg build/license metadata for redistribution.
RUN rm -f /usr/bin/pebble \
    && rm -rf /usr/share/man /usr/share/info \
    && find /usr/share/doc -type f ! -name copyright -delete
FROM scratch AS runtime-base
COPY --from=runtime-rootfs / /
ENV NVIDIA_DRIVER_CAPABILITIES=compute,video,utility
