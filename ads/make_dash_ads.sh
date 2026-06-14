#!/usr/bin/env bash
#
# make_dash_ads.sh — Re-encode ad clips into DASH VOD assets for livesim2.
#
# For every input .mp4 found under INPUT_ROOT it produces a DASH VOD asset with:
#   - one video AdaptationSet with 3 H.264 representations:
#       V4: 1280x720 @ 4000 kbps
#       V2:  960x540 @ 2000 kbps
#       V1:  640x360 @ 1000 kbps
#   - one audio AdaptationSet: AAC-LC stereo @ 96 kbps, 48 kHz, language eng
#     (silence is generated if the source has no audio track)
#   - 2 s closed GoPs / 2 s segments (5 segments for a 10 s clip)
#   - a burned-in overlay on every video variant showing:
#       name / resolution / framerate / bitrate / "seconds;frame"
#
# The source frame rate (e.g. 24 or 30 fps) is detected and preserved.
# mdhd language is set to "eng"; the MPD lang is rewritten to "en".
#
# Usage:
#   ./make_dash_ads.sh [INPUT_ROOT] [OUT_ROOT]
# Defaults: INPUT_ROOT=input_videos  OUT_ROOT=dash
#
set -euo pipefail

# ---- Tools ------------------------------------------------------------------
# drawtext requires an ffmpeg built with libfreetype. The plain Homebrew
# "ffmpeg" formula drops it, so prefer ffmpeg-full; allow override via $FFMPEG.
FFMPEG="${FFMPEG:-}"
if [[ -z "$FFMPEG" ]]; then
    for c in /opt/homebrew/Cellar/ffmpeg-full/*/bin/ffmpeg /opt/homebrew/bin/ffmpeg-full; do
        [[ -x "$c" ]] && { FFMPEG="$c"; break; }
    done
    [[ -z "$FFMPEG" ]] && FFMPEG="ffmpeg"
fi
FFPROBE="${FFPROBE:-ffprobe}"
MP4BOX="${MP4BOX:-MP4Box}"
FONT="${FONT:-/System/Library/Fonts/Supplemental/Arial.ttf}"

if ! "$FFMPEG" -hide_banner -filters 2>/dev/null | grep drawtext >/dev/null; then
    echo "ERROR: '$FFMPEG' has no drawtext filter (needs libfreetype). Set \$FFMPEG." >&2
    exit 1
fi

INPUT_ROOT="${1:-input_videos}"
OUT_ROOT="${2:-dash}"

# ---- Encoding ladder --------------------------------------------------------
# id  width height bitrate(k)
LADDER=(
    "V4 1280 720 4000"
    "V2 960  540 2000"
    "V1 640  360 1000"
)
SEG_DUR=2          # seconds per segment / GoP

# drawtext for one static line: text ypos color
dt() { echo "drawtext=fontfile='${FONT}':text='$1':fontcolor=$3:fontsize=${FS}:box=1:boxcolor=black@0.55:boxborderw=6:x=14:y=$2"; }

encode_asset() {
    local src="$1" name="$2" outdir="$3"
    local work; work="$(mktemp -d)"
    trap 'rm -rf "$work"' RETURN

    # --- probe source -------------------------------------------------------
    local fps_raw fps dur has_audio
    fps_raw="$("$FFPROBE" -v error -select_streams v:0 -show_entries stream=r_frame_rate -of csv=p=0 "$src" | head -1 | tr -dc '0-9/')"
    fps=$(( ${fps_raw%/*} / ${fps_raw#*/} ))          # integer fps (24 or 30)
    dur="$("$FFPROBE" -v error -show_entries format=duration -of csv=p=0 "$src")"
    has_audio="$("$FFPROBE" -v error -select_streams a -show_entries stream=index -of csv=p=0 "$src" | head -1)"

    local gop=$(( fps * SEG_DUR ))
    echo ">> $name  (${fps} fps, ${dur}s, audio=$([[ -n "$has_audio" ]] && echo yes || echo none))"

    # --- encode the 3 video renditions --------------------------------------
    local var id w h bk FS y tc vf
    for var in "${LADDER[@]}"; do
        read -r id w h bk <<<"$var"
        FS=$(( h / 26 )); [[ $FS -lt 12 ]] && FS=12     # scale font to height
        local lh=$(( FS * 7 / 5 ))                       # line height ~1.4*FS
        y=14
        tc="drawtext=fontfile='${FONT}':text='%{eif\\:floor(t)\\:d};%{eif\\:mod(n\\,${fps})\\:d\\:2}':fontcolor=yellow:fontsize=${FS}:box=1:boxcolor=black@0.55:boxborderw=6:x=14:y=$(( y + 4*lh ))"
        vf="scale=${w}:${h}"
        vf+=",$(dt "$name"        "$y"            white)"
        vf+=",$(dt "${w}x${h}"    "$(( y+lh ))"   white)"
        vf+=",$(dt "${fps} fps"   "$(( y+2*lh ))" white)"
        vf+=",$(dt "${bk} kbps"   "$(( y+3*lh ))" white)"
        vf+=",${tc}"

        "$FFMPEG" -hide_banner -nostdin -loglevel error -y -i "$src" -an \
            -vf "$vf" \
            -c:v libx264 -profile:v high -pix_fmt yuv420p \
            -b:v "${bk}k" -maxrate "$(( bk * 11 / 10 ))k" -bufsize "$(( bk * 2 ))k" \
            -r "$fps" -g "$gop" -keyint_min "$gop" -sc_threshold 0 \
            -force_key_frames "expr:gte(t,n_forced*${SEG_DUR})" \
            -x264opts "no-open-gop" \
            -movflags +faststart \
            "$work/${id}.mp4"
    done

    # --- audio: real track @ 96k, or generated silence ----------------------
    if [[ -n "$has_audio" ]]; then
        "$FFMPEG" -hide_banner -nostdin -loglevel error -y -i "$src" -vn \
            -c:a aac -b:a 96k -ar 48000 -ac 2 \
            -metadata:s:a:0 language=eng \
            -movflags +faststart "$work/A.mp4"
    else
        "$FFMPEG" -hide_banner -nostdin -loglevel error -y \
            -f lavfi -i "anullsrc=channel_layout=stereo:sample_rate=48000" \
            -t "$dur" -c:a aac -b:a 96k -ar 48000 -ac 2 \
            -metadata:s:a:0 language=eng \
            -movflags +faststart "$work/A.mp4"
    fi

    # --- DASH packaging ------------------------------------------------------
    rm -rf "$outdir"; mkdir -p "$outdir"
    "$MP4BOX" -quiet \
        -dash $(( SEG_DUR * 1000 )) -frag $(( SEG_DUR * 1000 )) -rap -bound \
        -profile live -mpd-title "$name" \
        -segment-name '$RepresentationID$/$Init=init$$Number$' \
        -segment-ext m4s -init-segment-ext mp4 \
        -out "$outdir/manifest.mpd" \
        "$work/V4.mp4#video:id=V4" \
        "$work/V2.mp4#video:id=V2" \
        "$work/V1.mp4#video:id=V1" \
        "$work/A.mp4#audio:id=A"

    # MPD lang: mdhd carries ISO-639-2 "eng"; DASH wants BCP-47 "en".
    sed -i '' -E 's/lang="eng"/lang="en"/g' "$outdir/manifest.mpd"
    # Tag the MPD with the Single-Period-Static profile (Ed-6) for SGAI ImportedMPD use.
    sed -i '' 's#profiles="[^"]*"#profiles="urn:mpeg:dash:profile:sps:2024"#' "$outdir/manifest.mpd"

    # Clamp the announced duration down to a whole number of segments when the media
    # overshoots one by a fraction (AAC priming): with $Number$ + @duration addressing,
    # players request ceil(dur/segdur) segments, so a 5 ms overshoot makes them ask for
    # one segment more than exists (a 404 at the end of every ad).
    python3 - "$outdir/manifest.mpd" "$SEG_DUR" <<'PYEOF'
import re, sys
path, seg = sys.argv[1], float(sys.argv[2])
txt = open(path).read()
def parse(d):
    m = re.fullmatch(r'PT(?:(\d+)H)?(?:(\d+)M)?([\d.]+)S', d)
    return int(m.group(1) or 0)*3600 + int(m.group(2) or 0)*60 + float(m.group(3))
m = re.search(r'mediaPresentationDuration="([^"]+)"', txt)
dur = parse(m.group(1))
over = dur % seg
if 0 < over < 0.1:
    txt = txt.replace(m.group(1), 'PT%gS' % (dur - over))
    open(path, 'w').write(txt)
PYEOF

    echo "   -> $outdir/manifest.mpd"
}

# ---- Drive over all inputs --------------------------------------------------
shopt -s nullglob
found=0
while IFS= read -r -d '' src; do
    found=1
    name="$(basename "$src")"; name="${name%.*}"
    encode_asset "$src" "$name" "$OUT_ROOT/$name"
done < <(find "$INPUT_ROOT" -type f -name '*.mp4' -not -path '*/.*' -print0 | sort -z)

[[ $found -eq 0 ]] && { echo "No .mp4 files found under '$INPUT_ROOT'" >&2; exit 1; }
echo "Done. Assets in '$OUT_ROOT/'."
