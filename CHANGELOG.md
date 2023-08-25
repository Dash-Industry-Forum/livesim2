# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Prometheus counters and histograms for request timing

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

[Unreleased]: https://github.com/Dash-Industry-Forum/livesim2/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/Dash-Industry-Forum/livesim2/compare/v0.5.1...v0.6.0
[0.5.1]: https://github.com/Dash-Industry-Forum/livesim2/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/Dash-Industry-Forum/livesim2/releases/tag/v0.5.0
