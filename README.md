![Test](https://github.com/Dash-Industry-Forum/livesim2/workflows/Go/badge.svg)
[![golangci-lint](https://github.com/Dash-Industry-Forum/livesim2/actions/workflows/golangci-lint.yml/badge.svg)](https://github.com/Dash-Industry-Forum/livesim2/actions/workflows/golangci-lint.yml)
[![GoDoc](https://godoc.org/github.com/Dash-Industry-Forum/livesim2?status.svg)](http://godoc.org/github.com/Dash-Industry-Forum/livesim2)
[![Go Report Card](https://goreportcard.com/badge/github.com/Dash-Industry-Forum/livesim2)](https://goreportcard.com/report/github.com/Dash-Industry-Forum/livesim2)

# livesim2 - the second generation DASH Live Source Simulator

`livesim2` is a new and improved version of the
[DASH-IF live source simulator][1].

As the original simulator ([livesim1][1]), the output is a wall-clock (UTC) synchronized
infinite linear stream of segments. The synchronization is done modulo asset length,
for example: a 1-hour asset restarts every hour on full hours, and a 30s asset
restarts every 30s on full and half minutes. If there is a clock in the video, it is
therefore easy to directly see how long the system delay is from publishing to
screen presentaiton. The very short example assets bundled with the code are only
8s long, which means that they restart every time the UTC time is a multiple of 8s,
relative to the Epoch start 1970-01-01:00:00:00Z.

To provide full UTC time stamps and the possibility to test subtitles,
livesim2 has a new feature for generating subtitles for any number of languages.
This is done by a URL parameter like `/timesubsstpp_en,sv` which will result in
two subtitle tracks with with language codes "en" and "sv", respectively.

The new software is written in Go instead of Python and designed to handle
content in a more flexible and versatile way.

It is intended to be very easy to install and deploy locally
since it is compiled into a single binary that serves the content via a built-in
performant HTTP/2 server.

There is also a tool called `dashfetcher` that can be used to download
DASH VoD assets that can serve as sources for the live
linear outputs.

The sources are looped so that an infinite "live" stream is available.

Similarly to [livesim1][1], the output is highly configurable by adding parameters inside the URLs.
These parameters are included not only in the MPD requests, but in
all segment requests allowing the server to be stateless, and
be able to generate streams with a huge number of
parameter variations. Currently, only a small subset of
all parameters of [livesim1][1] are implemented, but there are also new
parameters like the generated subtitles mentioned above.

## Components

There are two main components, the server `livesim2` and the VoD fetcher
`dashfetcher`.

### dashfetcher tool

The tool `dashfetcher` fetches DASH VoD assets via HTTP given an MPD URLs.
Currently it supports MPDs with SegmentTimeline with `$Time$` and
SegmentTemplate with `$Number$`. The assets must have no explicit `<BaseURL>` elements to
work properly. With the `-a/--auto` option, the path to the asset is preserved
as much as possible and adapted to the local path.

Files already downloaded will not be downloaded again, unless `-f/--force` is
used. As an example, to download a CTA-WAVE asset one can run

```sh
dashfetcher --auto https://dash.akamaized.net/WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17/stream.mpd
```

which will result in a locally stored DASH VoD asset in the directory

```sh
./vod/WAVE/vectors/cfhd_sets/12.5_25_0t3/2022-10-17/
```

with an MPD called `stream.mpd` and the segments stored in subdirectories named after their relative
URLs. The download URL is added to a file `mpdlist.json` which is read by livesim2, to provide
information about the asset.

One can have multiple MPDs in the same asset directory and they may share some representations.
That is an easy way to have variants with different representation combinations.

### livesim2 server

The server is configured in one or more ways in increasing priority:

1. Default values
2. In a config file
3. Via command-line parameters
4. With environment variables

Major values to configure are:

* the HTTP port being used (default: 8888)
* the top directory `vodroot` for searching for VoD assets to be used
* `certpath` and `keypath` is the HTTPS is used

Once the server is started, it will scan the file tree starting from
`vodroot` and gather metadata about all DASH VoD assets it finds.
Currently, only VoD assets using SegmentTimeline with `$Time$` and
SegmentTemplate with `$Number$`  are supported.

Once the server has started, it is possible to find out information about the server and
the assets using the HTTP endpoint

* /

that in turn points to:

* /config
* /healthz
* /assets
* /metrics

It is also possible to explore the file tree and play Vod assets by starting at

* /vod/...

Finally, any VoD MPD like `/vod/cfhd/stream.mpd` is available as a live stream by
replacing `/vod/` with `livesim2`as like `/livesim2/cfhd/stream.mpd`.

### MPD Restrictions

The following restrictions apply to the VoD manifest to be used with livesim2

* live-profile (separate segments)
* one Period with all representations of "same" duration
* no BaseURL elements
* no Location elements
* initialization and media attributes in SegmentTemplate on AdaptationSet level

### Test parameters

The query string parameter `?nowMS=...` can be used in any request
to set the wall-clock time `livesim2` uses as local time. The time is measured with respect to
the 1970 Epoch start, and makes it possible to test time-dependent requests in a deterministic way.

## Get Started

Install Go 1.19 or later.

Optionally, create a local `vendor` directory to keep a local copy of
all dependencies.

Then run

```sh
> go mod tidy
```

to fetch and install all dependencies.

To build `dashfetcher` and `livesim2` you can use the `Makefile` like

```sh
> make build
```

to create binaries in the /out directory with embedded version numbers.

During development it may be easier to use the usual go commands:

```sh
> cd cmd/dashfetcher
> go build .
> cd ../../cmd/livesim2
> go build .
```

or compile and run directly with `go run .`.

### Bundled test streams with the livesim2 server

A few very short (8s) test assets are bundled with the code.
These makes it possible to start the server and get live output by running

```sh
> cd cmd/livesim2
> ./livesim2 --vodroot=app/testdata/assets
```

The log will list the available assets and the port where the server runs.

They can then be streamed via URLs like:

```link
http://localhost:8888/livesim2/WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17/stream.mpd
http://localhost:8888/livesim2/testpic_2s/Manifest.mpd
http://localhost:8888/livesim2/testpic_8s/Manifest.mpd
```

The default pattern provides MPDs with SegmentTemplate using `$Number$`. To stream with
SegmentTimeline with `$Time$`, one should add the parameter `/segtimeline_1` between
`livesim2` and the start of the asset path. Other parameters are added in a similar way.

Adding longer assets somewhere under the `vodroot` results in longer loops.
All sources are NTP synchronized (using the host machine clock) with a initial start
time given by availabilityStartTime and wrap every sequence duration after that.

### Running dashfetcher

The `dashfetcher` binary can be found in as `out/dashfetcher` after `make build`.

```sh
> dashfetcher --help
```

will provide a long help text that explains how to use it and will also provide an example URL
to CTA-WAVE content.

## Running tests

The unit tests can be run from the top directory with the usual recursive Go test command
`go test ./...` or with the make targets for testing, linting, and coverage:

```sh
> make test
> make check
> make coverage
```

## Deployment

Both `dashfetcher` and `livesim2` can be compiled to single binaries
on any Go compiler target platform such as Windows, MacOS, and Linux.
Since the result is a single binary it is easy to start it anywhere.

On Linux, `livesim2` can be run as a `systemd` service.
More information can be found in the `./deployment` directory.

## HTTPS and HTTP/2

HTTPS and HTTP/2 are both supported. To enable TLS encryption, the two parameters
`certpath` and `keypath` must be set to point to a TLS certificate and a private
key file, respectively. It is also recommended to set the port to the default HTTPS
port 443.

## List of functionality and options

Start the server and access the root URL like `http://localhost:8888/` to
get a page with information about URL options available.

## Sponsoring

This project is in need of sponsoring for further development. See the
[SPONSORING.md](SPONSORING.md) file for more information.

## License

See [LICENSE.md](LICENSE.md).

[1]: (https://github.com/Dash-Industry-Forum/dash-live-source-simulator)
