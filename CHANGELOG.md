# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- parameter `mup` to set minimumUpdatePeriod in MPD
- new parameter `subsstppreg` can set vertical region
- new parameter `ltgt` sets latency target in milliseconds
- parameter `utc` to set one, multiple, or zero UTCTiming methods

### Fixed

- PublishTime now reflects the last change in MPD in ms and not current time.

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
- supports SegmentTempalte with $Number$
- supports low-latency mode with on-the-fly chunking
- features and URLs listed at livesim2 root page
- configurable generated stpp subtitles with timing info

[Unreleased]: https://github.com/Dash-Industry-Forum/livesim2/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/Dash-Industry-Forum/livesim2/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/Dash-Industry-Forum/livesim2/releases/tag/v0.5.0