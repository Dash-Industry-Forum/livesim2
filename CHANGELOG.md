# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- New URL option `svta_` for SVTA2053 Ad Creative Signaling (payload version 2, issue #310). Ad-creative
  windows of the live timeline are marked with an `EventStream` of scheme
  `urn:svta:advertising-wg:ad-creative-signaling`, one `Event` per creative whose node data is the v2 JSON
  payload: the creative's identifiers, its duration and the tracking URLs to fire while it plays. It uses
  the same break-schedule grammar as `sgai_` (`svta_30:15,90:15` or `svta_p60:20`), with `;ads=<n>` to
  split a break into several creatives, `;skip=`, `;click=`, `;verif=` and `;pod=` for the rest of the
  payload, and `;ts=` for the `EventStream@timescale` — for example
  `/livesim2/svta_p60:20;ads=2/testpic_2s/Manifest.mpd?sessionId=alice`. As for `sgai_`, the video track
  serves the generated AD BREAK countdown slate inside each window, so the signaled creative is visible.
  See the [README](README.md#svta2053-ad-creative-signaling).
- Each signaled creative carries the VAST-named tracking events `impression`, `start`, `firstQuartile`,
  `midpoint`, `thirdQuartile`, `complete`, `pause` and `resume`, plus `clickTracking` with `;click=1`. None
  of them carry an offset, so per SVTA2053-1 §4.4.5 each fires according to the semantics of its type: the
  timeline points where their names say, `pause` and `resume` when the viewer interrupts the ad. The URLs
  point back at livesim2's own `/sgai/beacon` endpoint with the session and break ids baked in (a player
  fires them verbatim), so the whole ad-measurement round trip can be watched live at
  `/sgai/session_status`. An identical beacon for the same ad, event and break occurrence is collapsed
  within a short window, the interaction events excepted, since a viewer can pause and resume repeatedly
  inside one creative. Verified with stock Shaka Player 5.2.9, the first release that reports the signaling
  of every break and treats an ad reaching its playout limit as complete.
- New `svta=<0|1>` setting on the `sgai_` option. With `sgai_...;svta=1` every ad Period of the List MPD
  returned by the ad-decisioning endpoint also gets an SVTA2053 `EventStream` describing the creative that
  Period imports, with its real catalog id and duration and the same tracking set as above. That set is a
  superset of what a DASH callback event can express, which is the reason to want it: `start` would need a
  second callback event at the same presentation time as the impression, and an interaction has no
  presentation time at all. Opt-in, since a player acting on both it and the existing callback beacons
  reports each timeline point twice.

- The `scte35_` URL option takes the same ad-break schedule grammar as `sgai_` and `svta_`, with
  `;key=val` options for the rest: `scte35_p60:20@10;cmd=timesignal;seg=break,po;ads=2;mpd=bin`.
  `cmd=` picks `splice_insert()` or `time_signal()`, `seg=` the segmentation levels (`break`, `po`,
  `dpo`, `ad`, `dad`, `promo`, `dpromo`, outermost first) and `ads=` splits the break into that many
  creative segments, so a Break holding a Placement Opportunity holding the individual ads is
  signaled the way SCTE 35 Figure 5 describes it: every level that opens at the same instant travels
  in one message, as consecutive segmentation descriptors, with the start and the end of each level
  sharing its `segmentation_event_id`. The rest of the options are `emsg=`, `mpd=`, `lead=`, `end=`,
  `repeat=`, `upid=`, `value=` and `ts=`. See the [README](README.md#scte-35-ad-avail-signaling).
- SCTE-35 messages can now also be carried in the MPD, which livesim2 had no support for at all:
  `mpd=bin` adds an `EventStream` of scheme `urn:scte:scte35:2014:xml+bin` with the message as
  `<Signal><Binary>`, and `mpd=xml` one of scheme `urn:scte:scte35:2013:xml` with the full
  `<SpliceInfoSection>`. DASH-IF IOP-5 §5.5 makes MPD events the carriage an ad-insertion MPD
  manipulator is expected to read. The events appear one `lead` time before their splice point, the
  same moment the inband cue is delivered, and are kept while the segments they cover are available.
  A message that closes a break carries `duration="0"`, as SCTE 214-1 §6.7.2.1 asks, rather than no
  `@duration` at all, which DASH defines as an unknown duration.
- The periodic ad-break schedule takes in-cycle offsets, `p<period>:<dur>[@<off>,...]`, for `sgai_`
  and `svta_` as well as `scte35_`: `p60:10@10,40` is a 10 s break 10 s and 40 s after every full
  UTC minute.
- `scte35_` can be combined with `sgai_` and `svta_` when they use the same break schedule, so the
  SCTE-35 cue announces the avail that an Alternative-MPD event fills with a real ad pod and
  SVTA2053 describes for measurement.

### Fixed

- The SCTE-35 messages generated for the `scte35_` URL option carried `pts_adjustment = 2^33 - pts_time`
  instead of zero, so the splice time a receiver computes as `(pts_time + pts_adjustment) mod 2^33` was
  always 0. The intended time was only visible in the `emsg` header. The splice time is now set on the
  `splice_info_section` rather than on the `splice_insert()` command, which is what gots derives
  `pts_adjustment` from, and a regression test decodes the generated payload to check both fields.

### Changed

- dash-mpd dependency bumped for the `EventType.Duration` pointer, which is what lets a zero
  `Event@duration` reach the MPD.
- The legacy `scte35_1|2|3` presets are now shorthands for `p60:20@10`, `p60:10@10,40` and
  `p60:10@10,36,46` in the new grammar. They emit the same `splice_insert` cues as before, but the
  schedule is anchored to the wall clock rather than to the availabilityStartTime, so a stream
  shifted with `start_` or `startrel_` places its breaks at the same UTC seconds as every other
  session (with the default `availabilityStartTime` at the epoch, nothing changes).
- The ad-break schedule (fixed or periodic breaks, the break instances signaled in an MPD, and the AD BREAK
  slate window) is now shared between `sgai_` and `svta_` in `AdBreaks`. The signaled set of a periodic
  schedule additionally keeps breaks that ended but are still inside the timeshift buffer for `svta_`, so a
  viewer seeking back still sees the ad-creative signaling.

## [1.13.0] - 2026-08-11

### Added

- Optional mode field on `timecc608`, whose value is now `<channel>-<lang>[-<mode>[-<modifier>]]`, choosing
  the CTA-608 caption mode: `paint` (the default), `pop`, `pop-sc`, `roll2` or `roll3`. Paint-on and roll-up
  type the caption out two characters per frame — paint-on onto a screen cleared at the start of each
  second, roll-up onto the base row of a 2- or 3-row window that scrolls the earlier lines up — and both
  keep every caption inside the segment that carries it, so segments stay independently decodable for a
  client that starts, seeks or joins mid-stream. Roll-up is limited to 2 or 3 rows because the caption's own
  lines put its base row at row 3. Generated by go-608 v0.9.0's `generate.BuildUnitPaintCues` and
  `BuildUnitRollUpCues`.

### Changed

- `timecc608` defaults to paint-on instead of pop-on, so by default no segment's captions depend on its
  neighbours. Pop-on remains available as `-pop`, with `-pop-sc` for the self-contained placement; a bare
  `-sc` (which used to ask for that) is rejected with a message pointing at `-pop-sc`.
- The `timecc608` language must be a three-letter ISO 639-2 code (`eng`, `swe`), the form the SCTE 214-1
  CEA-608 accessibility descriptor value is defined in, and the only place this option's language is used.
  RFC 5646 tags that DASH's `@lang` accepts — `en`, `en-US`, `zh-Hans` — were previously allowed through
  into `value="CC1=..."` and are now rejected. With the language a single field, an unknown mode or
  modifier is reported instead of being read as part of it.
- mp4ff dependency bumped to v0.55.0 and go-608 to v0.9.0
- The CTA-608 cue scheduling for `timecc608` is back to go-608's per-unit builders, whose v0.8.0
  `generate.Unit` (number, start time and frame count as independent inputs) and `WithFlipAtCueStart`
  cover both pop-on flip placements livesim2 needs, so the local copy of that scheduler is gone. Same
  bytes on the wire.
- A `timecc608` caption cue is never shorter than a second now that go-608 v0.9.0 divides a segment down
  into whole periods: a segment whose duration is not a multiple of a second, e.g. 1.92 s, gets one cue
  covering it instead of two shorter ones. Segments of 2 s and 2.002 s are unaffected.

### Fixed

- In-band CTA-608 captions in pop-on mode (`timecc608_CC1-eng-pop`) are now displayed over exactly the
  interval their text names, instead of appearing 0.6-0.75 s into it. Each cue's flip rides its cue's first
  frame, with the build sent over the preceding frames. Such captions consequently span segment boundaries,
  which costs a client that starts or seeks mid-stream its first cue period — see the
  [README](README.md). The new default mode, paint-on, has neither problem.

## [1.12.0] - 2026-07-23

### Added

- AV1 video support: assets with `av01`/`av1C` sample entries are recognized as video and served,
  and AV1 segments can be encrypted on the fly for both `cenc` and `cbcs` schemes. Per-segment
  common-encryption protection ranges (clear OBU/tile headers, protected tile data) are computed by
  mp4ff via `mp4.EncryptFragments`, which builds a fresh AV1 frame-header decoder per segment so
  concurrent requests are race-free. A new `testpic_2s_av1` test asset (AV1 1280x720@25 at 400 and
  600 kbps, using moqlivemock content, + AAC) exercises the path.
- In-band CTA-608 (CEA-608) closed captions: the `timecc608_<channel>-<lang>` URL option (e.g.
  `timecc608_CC1-eng`) injects a caption into the AVC/HEVC video elementary stream itself, showing a
  ticking UTC clock and the segment number on channel CC1, frame-accurate to wall-clock. The caption is
  built by the shared [go-608](https://github.com/Eyevinn/go-608) `generate.BuildUnitCues` helper (one
  self-contained pop-on cue per ~second, distributed one 608 pair per frame in presentation order) and
  carried as `user_data_registered_itu_t_t35` SEI on every frame, on both the default whole-segment path
  and the low-latency chunked path. The video AdaptationSet is advertised with a
  `urn:scte:dash:cc:cea-608:2015` `Accessibility` descriptor. The option cannot be combined with
  encryption and is rejected (HTTP 400) for assets that already carry CEA-608/708 captions (detected from
  the source MPD descriptor or existing `cc_data` SEI at scan time). Surfaced in the `/urlgen/` form.
- New test asset `testpic_2s/cea608.mpd` (video representation `V300_with_cc1_and_cc3`, `CC1=eng;CC3=swe`)
  carrying real in-band CEA-608 captions, used as the "already captioned" reject case.

### Changed

- Bumped `github.com/Eyevinn/mp4ff` to v0.54.0 for AV1 common-encryption (cenc/cbcs) support and
  the immutable-`InitProtectData` / `EncryptFragments` API; segment encryption now calls
  `mp4.EncryptFragments` per segment instead of looping `EncryptFragment`.

## [1.11.0] - 2026-06-30

### Added

- DASH Content Steering (ISO/IEC 23009-1 6th ed. §K.3.6, ETSI TS 103 998): the `steer_` URL option
  advertises several service locations ("CDNs") that point back to this server and adds a
  `<ContentSteering>` element; the steering endpoint returns a `PATHWAY-PRIORITY` manifest with a
  configurable `ttl=` and `mode=trigger` (hold until switched) or `mode=rotate`. Per-session,
  per-CDN segment requests are tracked and switchable via the `/api/steering/…` API and a live
  `/steering/session_status` monitor page; client steering messages
  (`_DASH_pathway`/`_DASH_throughput`) are verified and off-pathway clients flagged. An optional
  `csid_<group>` token groups sessions under one shared decision. The `/urlgen/` form has a Content
  Steering section and the index page links to the monitor.
- DASH XLink for multi-period: the `xlink_1` URL option activates the replacement of completed past
  periods elements with remote elements using XLink elements with `xlink:href="onRequest"`.
  The XLink urls for each completed period target the same endpoint as the manifest and adding the
  `?period=<period_id>` query parameter, which is interpreted by livesim2 to return the corresponding Period element.

### Changed

- URL generator: DRM options now follow the selected asset and are disabled for pre-encrypted assets.
- The SGAI and Content Steering live monitor pages (`/sgai/session_status`,
  `/steering/session_status`) now follow the system light/dark setting (via Pico CSS) like the
  other web pages, with theme-aware status colours for legibility on both backgrounds, while
  staying full-width and compact for their denser tables. Their shared styles and JS helpers
  (`esc`/`fmtTime`/`setDot`) are factored into `static/session_status.css` and
  `static/session_status_common.js`.
- Migrated the HTML web pages (welcome, assets, VoD assets, URL generator, SGAI
  session status) from Go's `html/template` to type-safe [templ](https://templ.guide)
  components (`*.templ` → generated `*_templ.go`). The `text/template` XML subtitle
  templates (`stpptime.xml`, `stpptimecue.xml`) are unchanged. Building now runs
  `templ generate` (see `make templ`); a `make templ-check` target and a pre-commit
  hook (triggered by both `*.templ` and `*_templ.go` changes) keep the generated code in
  sync, and the generated `*_templ.go` files are excluded from `golangci-lint`.
- Updated `github.com/go-chi/chi/v5` from v5.2.2 to v5.2.4.

### Fixed

- cmaf-ingest-receiver tests: replaced fixed `time.Sleep` waits for the receiver's asynchronous
  writes with polling (`EventuallyWithT`), fixing intermittent failures on the Windows CI runner.
- Hardened the `esc()` helper on the SGAI session-status page (added in v1.10.0) to also escape
  `"` and `'`, so values interpolated into double-quoted HTML attributes cannot break out of the
  attribute (attribute XSS); matches the same hardening on the content-steering status page.

## [1.10.0] - 2026-06-15

### Changed

- Upgraded the project to Go 1.25 (`go.mod`, GitHub Actions workflows, and the `Dockerfile` build stage).
- Default `--playurl` reference player switched from `dash.js/latest` to `dash.js/nightly`,
  since `latest` currently ignores the MPD URL parameters passed via `?mpd=`.

### Added

- Server-Guided Ad Insertion (SGAI) per DASH Ed.6 Alternative-MPD: `sgai_` URL option
  injects Replace events (`urn:mpeg:dash:event:alternativeMPD:replace:2025`) with
  Annex I RequestParam; `/sgai/ads` returns a personalized List MPD for an
  interest-steered request (interest steering, per-session rotation within a match,
  duration fit); ad creatives are SPS VoD assets under `<vodroot>/ads/` with optional
  `ads.json` tags; `/sgai/beacon` records impression + quartile beacons; session store
  with `/api/sgai/ads`, `/api/sgai/sessions[/{sid}]` APIs and a live
  `/sgai/session_status` monitor page.
- Periodic SGAI ad breaks: `sgai_p<period>:<dur>` schedules a break at every wall-clock
  multiple of the period (e.g. `p60:20` = 20 s at every full UTC minute), with windowed
  event signaling and stable per-occurrence ids; late joiners land mid-break.
- Generated "AD BREAK" countdown slate served on the video track inside SGAI break
  windows: encoded with hi264 against the representation's own SPS/PPS (IDR per second
  + P_Skip), mirroring the replaced segment's frame timing and per-sample sizes
  (filler-NALU padding) so frame rate and bitrate match; audio untouched.
- An ad pod is only generated for an interest-steered request: with no `interests=` (the
  default — e.g. a plain stream with no sessionId/interests) or interests that match no
  ad, `/sgai/ads` answers 404 and the break stays unfilled, so the viewer keeps the
  underlying AD BREAK slate (the base ad).
- `Cache-Control: no-store` on ad creatives (`/vod/ads/*`) and List MPD responses.
- SGAI section on the urlgen page; SGAI mentioned on the landing page and in the
  OpenAPI description; ad creatives filtered out of the urlgen asset list.
- Two minimal example ad creatives bundled under the test vodroot
  (`testdata/assets/ads/`): `train_ad` (travel) and `gotland_runt_ad`
  (sailing, boats), each a single 640×360 representation plus audio, so SGAI
  works out of the box and `interests=travel,sailing` yields a two-ad pod.
- README section documenting SGAI: the `sgai_` schedule option, ad creatives
  and `interests`/`sessionId` personalization, the pod-selection and AD BREAK
  slate behavior, and the monitoring endpoints.
- Honor the `X-Forwarded-Proto` header (set by a fronting proxy / CDN) when constructing the scheme in MPD `Location` and `BaseURL` elements. Only `http` and `https` are accepted; other values fall back to local TLS detection.

### Fixed

- Misleading `--writerepdata` description in the CLI help and README: the flag always
  (re)generates and overwrites representation metadata, but was documented as "if not
  present". Corrected the help/README text and added the missing `--writemissingrepdata`
  flag to the README option list (use it to write only missing files). Behavior unchanged.
- IV reuse across fragments in `cenc` encryption mode by chaining the IV
  returned by `mp4.EncryptFragment` (Issue #295)

### Removed

- `--scheme` CLI flag. It was never read by the server and had no effect. Use `--host=https://example.com` (full scheme://host) for a hard override, or rely on `X-Forwarded-Proto` from your proxy.

## [1.9.0] - 2026-02-06

### Added

- VVC test content (testpic_2s_vvc)
- New `--writemissingrepdata` CLI option to generate RepData files only for representations that lack them (Issue #271)
- Version field in RepData structure to support future format evolution
- Support for DASH-IF Certurl ContentProtection element
- Pattern support for audio SegmentTimeline following DASH Ed. 6
- New URL options `segtimeline_pattern/` and `segtimelinenr_pattern/`
- URL generator page options for both pattern cases
- Documentation for CMAF Ingest

### Fixed

- Anchored regex for matching representation
- Security alerts from CodeQL
- Race conditions in CMAF Ingest tests

## [1.8.0] - 2025-09-11

### Added

- HEVC encryption support
- AC-4 audio support (including full encryption)
- MPEG-H audio support (including full encryption)
- AC-3 and EC-3 full encryption support
- Support for CMAF-compliant audio edit list
- Documented the CMAF edit list support for both audio and video

### Changed

- Minimal Go version increased to 1.23
- Updated dash-mpd to v0.13.0 (Full DASH Ed. 6 support)
- Updated mp4ff to v0.50.0 (more codecs support)

### Fixed

- One HTTP PUT with Content-Length in cmaf-ingest
- Fixed typo in urlgen template
- Lost error reporting in initial segment parsing
- Added `Access-Control-Expose-Header` to make UTCTiming HEAD work (Issue #251)

### Added

- Test asset with varying segment duration (4s and 8s) with exact 6s average
- Described how to use Dockerfile from Github Container repository (ghcr.io)

## [1.7.0] - 2025-01-17

### Changed

- CMAF ingest of full segments now send Content-Length header

### Added

- Basic Annex I support for announcing that MPD query parameters should be used in all video segment requests
- Verification that the MPD and the video segments carry the URL-specified query parameters

### Fixed

- endNumber in live MPD (Issue #235)
- urlgen page crashed when no DRM configuration
- mpd part of play URL in urlgen is now query escaped
- ContentProtect for DRMs in CPIX file with no configured LaURL
- Updated to golangci-lint v2 and fixed all remarks

### Chore

- updated dependencies

## [1.6.0] - 2024-12-03

### Added

- On-the-fly encryption with keys from commercial DRM (Widevine and PlayReady) via CPIX document
- DRM configuration and URL generation on `urlgen` page
- Unified ECCP and other DRMs using the new URL parameter `/drm_X`

### Fixed

- CLI parameter -h for livesim2

## [1.5.2] - 2024-11-05

### Fixed

- Segment size bug for ECCP encryption (introduced in v1.5.1)
- `--timeout` parameter not working. Changed to `--timeoutS`

## [1.5.1] - 2024-11-01

### Added

- Better logging when loading asset representation data
- Check that pre-encrypted content has the same duration for all representations
- Test that endNumber in SegmentTemplate will limit segments used
- Automatic build of Docker Images

### Fixed

- Pre-encrypted content is not re-fragmented, but left as it
- Dockerfile to insert version in binary (requires full repo or tag)

## [1.5.0] - 2024-10-02

### Added

- Added functions and constants for CMAF file extensions in pkg/cmaf
- Short HEVC + AC-3 test content
- Generation of CMAF ingest streams with a REST-based API
- New program `cmaf-ingest-receiver` that can receive one or more CMAF ingest streams
- New option `--whitelistblocks` for unlimited number of requests to some CIDR blocks
- Much improved `cmaf-ingest-receiver`
- Link on starting page to Wiki page on preparing content

### Fixed

- Will now set contentType from mimeType on AdaptationSet or Representation level.
- If contentType and mimedType is not present, contentType will be set from codecs string.
- Issue with audio resegmentation.

### Changed

- Go version changed to 1.22

## [1.4.1] - 2024-05-28

### Fixed

- publishTime of MPD for multiperiod with SegmentTimeline

## [1.4.0] - 2024-05-25

### Added

- More error logging for segment generation.
- New endpoint /version responds with livesim2 version
- Some more links in the Welcome page

### Fixed

- fix patch response for multiperiod segment-timeline
- fix publishTime for multiperiod SegmentTemplate with Number

## [1.3.1] - 2024-05-08

### Fixed

- correct low-latency MPD update time for SegmentTimeline

## [1.3.0] - 2024-04-23

### Added

- MPD Patch functionality with new `/patch_ttl` URL configuration
- nowDate query parameter as an alternative to nowMS for MPD and patch
- MPD Patch has Expires header equal to publishTime + ttl + 10s

### Fixed

- Timed stpp subtitles EBU-TT-D linePadding
- Update dependencies

## [1.2.2] - 2024-03-05

### Fixed

- Add extra CORS headers to fix ECCP key request

## [1.2.1] - 2024-03-04

### Fixed

- Correct UTCTiming schemes for http-iso and http-head
- Make urlgen fields for negative test cases easier to fill
- Fix OPTIONS for livesim2/ path
- Fix encryption of segments for representations with previous metadata

## [1.2.0] - 2024-02-16

### Added

- Support for DASH-IF Enhanced Clear Key Content Protection (ECCP)
- On the fly encryption for ECCP using cbcs or cenc scheme

### Fixed

- Make HTTP OPTIONS method work for all URLs
- Make --playurl work for general paths
- Derive and insert contentType if missing
- Remove any mehd box from init segment
- Asset lookup for case where one asset path is prefix of another

## [1.1.1] - 2024-01-19

### Fixed

- The UTCTiming output for xsdate is now the same as for ISO
- The DASH-IF content protection signaling updated to follow ECCP in IOP 5.0

## [1.1.0] - 2024-01-04

### Added

- UTCTiming "mode" `keep` forwards UTCTiming values from VoD MPD
- UTCTiming "modes" `httpisoms` and `httpxsdatems` for millisecond resolution
- Support for Marlin DRM and DASH-IF ClearKey in MPD

### Fixed

- Default UTCTiming signaling schemeIdUri set to "urn:mpeg:dash:utc:http-xsdate:2014"

## [1.0.1] - 2023-11-15

### Fixed

- Correct contentType match for subtitles (text)

## [1.0.0] - 2023-10-30

### Added

- New highly configurable `statuscode` parameter for cyclic bad segment request responses
- New URL parameter `traffic` to simulate periodic issues with fetching
  segments. Supports multiple parallel BaseURLs.
- Dockerfile to build a minimal Docker image with sample test content

### Changed

- Upgrade to Go 1.21
- Changed logging to slog instead of zerolog. Log levels limited to DEBUG, INFO, WARN, ERROR.

### Fixed

- Vertical spacing for buttons on web pages

## [0.9.0] - 2023-10-13

### Added

- Support for audio segments not matching video duration. Audio timing follows video by resegmentation
- Test content with 29.97fps with and without audio beeps
- /debug/pprof entry for profiling
- log url and location for redirected HTTP requests

### Changed

- The online player is now proxied via /player when livesim2 runs with http
- The online playURL should now including scheme
- repdata (representation data on disk) format extended with commonSampleDuration
- writerepdata option writes repdata even if existing

### Fixed

- added muted=true to default playURL
- HTTP 410 Gone response for segments before timeShiftBufferDepth
- limited methods in OPTIONS response

## [0.8.0] - 2023-09-22

### Added

- Prometheus counters and histograms for request timing
- Direct links to play assets mapped to latest dash.js with http or https scheme
- Timing-Allow-Origin header to enable more detailed timing in client
- /genurl page with URL generator supporting all URL parameters
- /reqcount page for checking requests per interval
- New option to log requests per interval to a file

### Changed

- The / page has been slightly rewritten
- The /assets and /vod pages slightly changed
- The request limit interval can now be configured

### Fixed

- utc-timing URLs use https scheme

## [0.7.0] - 2023-08-24

### Added

- much improved information web pages. Extracts MPD information from `ProgramInformation` inside MPDs.
- Full URLs to assets is listed and can be copied to clipboard from pages `/assets` and `/vod`
- support for `/scte35_x` URL config to insert periodic SCTE-35 emsg message (1, 2, or 3 per minute)
- server startup boost by loading (and writing) previously generated gzipped tar files with representation metadata
- new configuration parameters `repdataroot` and `writerepdata` to control this
- HTTP redirect from `/livesim` to `/livesim2` and `/dash/vod` to `/vod` for compatibility with livesim1
- support assets with stpp subtitles in both text and image format. New test content added
- support DASH-IF thumbnails including multi-period. New test content added
- new URL parameter `timesubswvtt` provides generated timing wvtt subtitles
- `timesubsstpp` and `timesubswvtt` now work with SegmentTimeline
- `continuous_1` URL parameter to signal multiperiod continuity
- Automatic Let's Encrypt certificates for HTTPS for one or more domains via `domains` parameter

### Fixed

- `/ato_inf` (infinite availability offset) now makes all segments in past and future available

## [0.6.0] - 2023-06-10

### Changed

- moved list URL parameters to [livesim2 wiki](https://github.com/Dash-Industry-Forum/livesim2/wiki/URL-Parameters)
- removed `scheme` and `urlprefix` configuration. Now replaced with `host` which overrides `scheme://host` in all generated URLs

### Added

- new URL parameter `periods` provides multiple periods (n <= 60 per hours, segment and period durations must be compatible)
- new URL parameter `segtimelinenr` turns on SegmentTimeline with `$Number$` addressing
- new URL parameter `mup` to set minimumUpdatePeriod in MPD
- new URL parameter `subsstppreg` can set vertical region
- new URL parameter `ltgt` sets latency target in milliseconds
- new URL parameter `utc` to set one, multiple, or zero UTCTiming methods
- new functionality to handle relative start and stop times by generating a Location element
- new config parameters `scheme` and `host` to be used in generated Location and BaseURL elements

### Fixed

- PublishTime now reflects the last change in MPD in ms and not current time.
- availabilityTimeOffset now gives the right PublishTime value for complete segments
- infinite availabilityTimeOffset for SegmentTimeline now results in an error
- Git version and date inserted properly when running "make build"
- livesim2 version header inserted in every HTTP response
- start-over case with `start` and `stop` time now provides proper dynamic and static MPDs

## [0.5.1] - 2023-03-09

### Fixed

- make `ato=inf` work, i.e. infinite availabilityTimeOffset

### Added

- list of complete MPD paths in /assets response

## [0.5.0] - 2023-03-07

### Added

- First public release and version
- `dashfetcher` tool to fetch a DASH asset online
- `livesim2` server to stream simulated DASH live
- supports SegmentTimeline with $Time$
- supports SegmentTemplate with $Number$
- supports low-latency mode with on-the-fly chunking
- features and URLs listed at livesim2 root page
- configurable generated stpp subtitles with timing info

[Unreleased]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.13.0...HEAD
[1.13.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.12.0...v1.13.0
[1.12.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.11.0...v1.12.0
[1.11.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.10.0...v1.11.0
[1.10.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.9.0...v1.10.0
[1.9.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.8.0...v1.9.0
[1.8.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.7.0...v1.8.0
[1.7.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.6.0...v1.7.0
[1.6.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.5.2...v1.6.0
[1.5.2]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.5.1...v1.5.2
[1.5.1]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.5.0...v1.5.1
[1.5.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.4.1...v1.5.0
[1.4.1]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.4.0...v1.4.1
[1.4.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.3.1...v1.4.0
[1.3.1]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.3.0...v1.3.1
[1.3.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.2.2...v1.3.0
[1.2.2]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.2.1...v1.2.2
[1.2.1]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v0.9.0...v1.0.0
[0.9.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v0.5.1...v0.6.0
[0.5.1]: https://github.com/Dash-Industry-Forum/livesim2/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/Dash-Industry-Forum/livesim2/releases/tag/v0.5.0
