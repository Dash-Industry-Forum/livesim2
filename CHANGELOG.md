# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Basic Annex I support for announcing that MPD query parameters should be used in all video segment requests
- Verification that the MPD and the video segments carry the URL-specified query parameters

### Fixed

- endNumber in live MPD (Issue #235)

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

[Unreleased]: https://github.com/Dash-Industry-Forum/livesim2/compare/v1.6.0...HEAD
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
