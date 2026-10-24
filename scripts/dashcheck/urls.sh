#!/bin/sh
# urls.sh prints the MPD URLs that run.sh checks with dashcheck, one per line.
#
# Usage: urls.sh <host> <profile>
#
#   host     the livesim2 server, e.g. http://localhost:8888 or https://livesim2.dashif.org
#   profile  testdata:   every MPD live, VoD, with segtimeline_1 and segtimelinenr_1, plus all URL options
#            production: every MPD live, VoD and with segtimeline_1, plus a smaller set of options
#
# The MPDs are taken from the server's /assets and /vod pages, so new content is checked
# without changing this script. /vod also lists the MPDs that cannot be made live. The options use testpic_2s, testpic_6s, testpic_2s_low_delay and
# testpic_2s_av1, which are both in cmd/livesim2/app/testdata/assets and on livesim2.dashif.org.
# DRM names the DRM package for the drm_ option (none if unset).
# Negative tests (statuscode_, traffic_, timeoffset_, modulo_) are left out, since they fail by design.
set -eu

if [ $# -ne 2 ]; then
	echo "usage: $0 <host> testdata|production" >&2
	exit 2
fi
H=${1%/}
PROFILE=$2
L=$H/livesim2
NOW=$(date +%s)
T=testpic_2s/Manifest.mpd

case $PROFILE in
testdata | production) ;;
*)
	echo "unknown profile $PROFILE" >&2
	exit 2
	;;
esac

# The MPD paths behind the Play links of a page, e.g. testpic_2s/Manifest.mpd
mpds() {
	curl -fsS "$H/$1" | grep -o "mpd=[^&\"]*/$2/[^&\"]*\\.mpd" | sed "s#.*/$2/##" | sort -u
}
live=$(mpds assets livesim2)
vod=$(mpds vod vod)
if [ -z "$live" ] || [ -z "$vod" ]; then
	echo "no MPDs found on $H/assets or $H/vod" >&2
	exit 1
fi

for m in $vod; do
	echo "$H/vod/$m"
done
for m in $live; do
	echo "$L/$m"
	echo "$L/segtimeline_1/$m"
	if [ "$PROFILE" = testdata ]; then
		echo "$L/segtimelinenr_1/$m"
	fi
done

opt() { echo "$L/$1/${2:-$T}"; }

# Options checked in both profiles
opt "ast_$((NOW - 3600))"
opt "startrel_-60"
opt "tsbd_60"
opt "segtimeline_pattern"
opt "inbandps_1"
opt "chunkdur_0.5/ato_1.5/ltgt_3000"
opt "periods_60"
opt "periods_60/segtimeline_1"
opt "timesubsstpp_en,sv/timesubswvtt_en"
opt "timecc608_CC1-eng"
opt "scte35_p60:20@10;mpd=xml"
opt "sgai_p60:20"
opt "svta_p60:20"
opt "eccp_cbcs"
opt "c2pa_vsi"
opt "ssras_2,1/chunkdurssr_2,0.5" testpic_2s_low_delay/Manifest.mpd
opt "eccp_cenc" testpic_2s_av1/Manifest.mpd
echo "$L/steer_alpha,beta;ttl=20/$T?sessionId=dashcheck"
echo "$L/annexI_a=1,b=2/$T?a=1&b=2"
if [ -n "${DRM:-}" ]; then
	opt "drm_$DRM"
fi

if [ "$PROFILE" = production ]; then
	exit 0
fi

# Timing and MPD
opt "start_$((NOW - 600))/stop_$((NOW + 1800))"
opt "start_$((NOW - 1200))/stop_$((NOW - 60))"
opt "startrel_-30/stoprel_600"
opt "mup_10"
opt "spd_8"
opt "snr_0"
opt "snr_-1"
opt "snr_1000"
for u in direct head ntp sntp httpxsdate httpxsdatems httpiso httpisoms none keep head-direct-httpiso; do
	opt "utc_$u"
done
opt "patch_60"
opt "patch_60/segtimeline_1"
opt "patch_60/segtimelinenr_1"

# Segment addressing and codecs
opt "segtimelinenr_pattern"
opt "inbandps_0"
opt "inbandps_1/segtimeline_1" testpic_6s/Manifest.mpd

# Low latency
opt "chunkdur_0.5/ato_1.5"
opt "chunkdur_0.2/ato_1.8/ltgt_2000/segtimeline_1"
opt "chunkdur_0.5/ato_1.5/segtimelinenr_1"
opt "chunkdur_1/ato_1" testpic_6s/Manifest.mpd
opt "ato_inf"
opt "ato_1"
opt "ltgt_3000"
opt "ssras_2,1/chunkdurssr_2,1" testpic_2s_low_delay/Manifest.mpd
opt "ssras_2,1/chunkdurssr_2,0.5/segtimeline_1" testpic_2s_low_delay/Manifest.mpd
opt "chunkdur_0.5/ato_1.5" testpic_2s_low_delay/Manifest.mpd
opt "chunkdur_0.5/ato_1.5" testpic_2s_av1/Manifest.mpd

# Multiple periods
opt "periods_60/continuous_1"
opt "periods_60/segtimelinenr_1"
opt "periods_60/continuous_1/segtimeline_1"
opt "periods_6/xlink_1"
opt "periods_60/xlink_1"

# Subtitles and captions
opt "timesubsstpp_en"
opt "timesubsstpp_en,sv/timesubsdur_1500/timesubssegnr_0/timesubsreg_1"
opt "timesubswvtt_en"
opt "timesubswvtt_en,sv/timesubsreg_1"
opt "timesubsstpc_en"
opt "timesubsstpc_en;nochange=1;body=1"
opt "timesubswvtc_en"
opt "timesubswvtc_en;nochange=1"
opt "timesubsstpp_en/segtimeline_1"
opt "chunkdur_0.5/ato_1.5/timesubsstpp_en/timesubsstpc_en"
opt "chunkdur_0.5/ato_1.5/timesubswvtt_en/timesubswvtc_en"
opt "timecc608_CC1-eng-roll3"
opt "timecc608_CC1-eng-pop"
opt "timecc608_CC1-eng-pop-sc"

# Ad signaling
opt "scte35_1"
opt "scte35_2"
opt "scte35_3"
opt "scte35_p60:20@10"
opt "scte35_p60:20@10;cmd=timesignal;seg=break,po;ads=2"
opt "scte35_p60:20@10;mpd=bin;pre=5"
opt "scte35_1/segtimeline_1"
opt "sgai_p60:20;svta=1"
opt "sgai_p60:20;skipafter=5;nojump=1"
opt "sgai_p60:20/segtimeline_1"
opt "scte35_1/sgai_p60:20@10"
opt "svta_p60:20;ads=2;pod=1;click=1;verif=1"
opt "svta_p60:20;remote=1"
opt "svta_p60:20/segtimeline_1"

# Content protection and signing
opt "eccp_cenc"
opt "eccp_cbcs/segtimeline_1"
opt "eccp_cbcs" testpic_2s_av1/Manifest.mpd
opt "c2pa_vsi/segtimeline_1"

# Content steering
echo "$L/steer_alpha,beta,gamma;ttl=20;mode=rotate;qbs=1/$T?sessionId=dashcheck2"
echo "$L/csid_groupA/steer_alpha,beta;ttl=20/$T?sessionId=dashcheck3"

# livesim1 parameters that are accepted but have no effect
opt "dur_60/init_2/tfdt_32/cont_1/etp_10/etpDuration_60/insertad_1/peroff_1/sidx_1/segtimelineloss_1"
