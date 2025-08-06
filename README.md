![Test](https://github.com/Dash-Industry-Forum/livesim2/workflows/Go/badge.svg)
[![golangci-lint](https://github.com/Dash-Industry-Forum/livesim2/actions/workflows/golangci-lint.yml/badge.svg)](https://github.com/Dash-Industry-Forum/livesim2/actions/workflows/golangci-lint.yml)
[![GoDoc](https://godoc.org/github.com/Dash-Industry-Forum/livesim2?status.svg)](http://godoc.org/github.com/Dash-Industry-Forum/livesim2)
[![Go Report Card](https://goreportcard.com/badge/github.com/Dash-Industry-Forum/livesim2)](https://goreportcard.com/report/github.com/Dash-Industry-Forum/livesim2)

# livesim2 - the second generation DASH Live Source Simulator

`livesim2` is a new and improved version of the
[DASH-IF live source simulator][1].

*Test it at [https://livesim2.dashif.org](https://livesim2.dashif.org) or set up your own server
using your own DASH content or test content available
at [livesim-content](https://github.com/Dash-Industry-Forum/livesim-content).
See the [wiki](https://github.com/Dash-Industry-Forum/livesim2/wiki) for more info.*

As the original simulator ([livesim1][1]), the output is a wall-clock (UTC) synchronized
infinite linear stream of segments. This achieved by looping input VoD DASH assets,
and changing time stamps so that an infinite "live" stream is available.
The synchronization is done modulo asset duration,
for example: a 1-hour asset restarts every hour on full hours, and a 30s asset
restarts every 30s on full and half minutes. If there is a clock display in the video, and
the length is full minutes or similar, it is
therefore easy to directly see how long the system delay is from publishing to
screen presentation. The very short example assets bundled with the code are only
8s long, which means that they restart every time the UTC time is a multiple of 8s,
relative to the Epoch start 1970-01-01:00:00:00Z.

To provide full UTC time stamps on-screen and the possibility to test subtitles,
livesim2 has a new feature for generating subtitles for any number of languages.
This is done by a URL parameter like `/timesubsstpp_en,sv` which will result in
two `stpp` (segmented TTML) subtitle tracks with with language codes "en" and "sv", respectively.
There is a corresponding setting for `wvtt` (segmented WebVTT) subtitles using `/timesubswvtt_en,sv`.

The new `livesim2` software is written in Go instead of Python and designed to handle
content in a more flexible and versatile way. It is intended to be very easy to install and deploy locally
since it is compiled into a single binary that serves the content via a built-in
performant HTTP/2 server. There is also a very simple way of setting up HTTPS
using Let´s Encrypt.

Similarly to [livesim1][1], the output is highly configurable by adding parameters inside the URLs.
These parameters are included not only in the MPD requests, but in
all segment requests allowing the server to be stateless, and
be able to generate streams with a huge number of
parameter variations. Currently, not all parameters of [livesim1][1] are implemented,
but there are also new parameters like the generated subtitles mentioned above.

The [URL wiki page][urlparams] lists what is available and the served page `/urlgen`
makes it easy to construct URLs to play the content with specific parameters set.

Beside `livesim2` there is a tool called `dashfetcher` in this repo.
That tool can be used to download the MPD and all segments of a DASH VoD asset.

## livesim2 server

The server is configured in one or more ways in increasing priority:

1. Default values
2. In a config file
3. Via command-line parameters
4. With environment variables

Major values to configure are:

* the top directory `vodroot` for searching for VoD assets to be used
* the HTTPS `domains` if Let's Encrypt automatic certificates are used
  * `certpath` and `keypath` if HTTPS is used with manually downloaded certificates
  * the HTTP/HTTPS `port` if `domains` is not being used (default: 8888)

Once the server is started, it will scan the file tree starting from
`vodroot` and gather metadata about all DASH VoD assets it finds.
Currently, only source VoD assets using SegmentTimeline with `$Time$` and
SegmentTemplate with `$Number$`  are supported.

### Command-line parameters

A complete list of parameters, and their access
via the command line looks like:

```sh
  --certpath string      path to TLS certificate file (for HTTPS). Use domains instead if possible
  --cfg string           path to a JSON config file
  --domains string       One or more DNS domains (comma-separated) for auto certificate from Lets Encrypt
  --host string          host (and possible prefix) used in MPD elements. Overrides auto-detected full scheme://host
  --keypath string       path to TLS private key file (for HTTPS). Use domains instead if possible.
  --livewindow int       default live window (seconds) (default 300)
  --logformat string     log format [text, json, pretty, discard] (default "text")
  --loglevel string      log level [DEBUG, INFO, WARN, ERROR] (default "INFO")
  --maxrequests int      max nr of request per IP address per 24 hours
  --playurl string       URL template to play mpd. %s will be replaced by MPD URL (default "https://reference.dashif.org/dash.js/latest/samples/dash-if-reference-player/index.html?mpd=%s&autoLoad=true&muted=true")
  --port int             HTTP port (default 8888)
  --repdataroot string   Representation metadata root directory. "+" copies vodroot value. "-" disables usage. (default "+")
  --reqlimitint int      interval for request limit i seconds (only used if maxrequests > 0) (default 86400)
  --reqlimitlog string   path to request limit log file (only written if maxrequests > 0)
  --scheme string        scheme used in Location and BaseURL elements. If empty, it is attempted to be auto-detected
  --timeout int          timeout for all requests (seconds) (default 60)
  --vodroot string       VoD root directory (default "./vod")
  --writerepdata         Write representation metadata if not present
```

### Quicker load by using metadata files

For assets with many segments, the scanning process can take a considerable time.
The possibility to generate and read extra representation metadata files has
therefore been added. For representation `repX`, the corresponding metadata file
is `repX_data.json.gz`. As the file extensions indicates, these files are gzipped
JSON files. To generate such files, the option `writerepdata` must be on.
The root directory for such files is by default the same as the VoD root directory,
meaning that the metadata files will be in the same directories as the corresponding
MPDs. However, it is possible to use another path, by specifying `repdataroot`.

Once the server has started, it is possible to find out information about the server and
the assets using the root HTTP endpoint

* /

that in turn points to:

* /urlgen
* /assets
* /config
* /healthz
* /metrics

and links to the Wiki page for more information.

It is also possible to explore the file tree and play Vod assets by starting at

* /vod/...

Finally, any VoD MPD like `/vod/cfhd/stream.mpd` is available as a live stream by
replacing `/vod/` with `livesim2` e.g. `/livesim2/cfhd/stream.mpd`.

### Backwards compatibility with livesim

For backwards compatibility with the first version of `livesim` where `/livesim` was used
as a prefix for simulated live output, and `/dash/vod` was the path to the VoD assets,
these two paths are redirected by the server with an HTTP 302 response as:

    /livesim/* -> /livesim2/*
    /dash/vod/* -> /vod/*

### MPD Restrictions

The following restrictions apply to the VoD manifest to be used with livesim2

* live-profile (separate segments)
* one Period with all representations of "same" duration
* no BaseURL elements
* no Location elements
* initialization and media attributes in SegmentTemplate on AdaptationSet level

### Special Time Test Parameter `nowMS`

The query string parameter `?nowMS=...` can be used in any request
to set the wall-clock time that `livesim2` uses as reference time. The time is measured with respect to
the 1970 Epoch start, and makes it possible to test time-dependent requests in a deterministic way.

## Get Started

Install Go 1.23 or later.

Then run

```sh
> go mod tidy
```

to fetch and install all dependencies.

To build `dashfetcher` and `livesim2` you can use the `Makefile` like

```sh
> make build
```

to create binaries in the `/out` directory with embedded version numbers.

During development it may be easier to use the usual go commands:

```sh
> cd cmd/dashfetcher
> go build .
> cd ../../cmd/livesim2
> go build .
```

or compile and run directly with `go run .`.

### HTTPS with automatic certificates

To enable HTTPS in an easy manner, make sure that you have DNS pointing to your machine,
and that ports 80 and 443 are forwarded. Then use the parameter
`--domains=your.domain.com,second.domain.com` to automatically fetch TLS certificates
from Let's Encrypt for your domains to this machine. The certificates are automatically
renewed before they expire.

#### HTTPS with manual certificates

The old-fashioned way of using manually acquired TLS certificates is also supported.
Use the two parameters `certpath` and `keypath` to point to the respective files,
and set the `port` to 443.`

## Content

The content must be a DASH VoD asset in `isoff-live` format
(individual segment files) with either `SegmentTimeline with $Time$` or
`SegmentTemplate with $Number$`. The video segments duration must have an
exact average duration so that the total duration is the number of segments
time that average. The total duration must be an integral number of milliseconds.
For example, an asset with alternating segment duration sof 8s and 4s, is fine
as long as there are an equal number of each leading so that the avererage duration is
6s. The segment duration is not allowed to vary more than 50% compared to that
average duration in order for livesim2 to be able to generate MPDs with SegmentTemplate
with \$Number\$.

The input audio segment do not need to follow the duration of the video segments,
although it is beneficial if they are the same, as for example 1.92s segments for
50fps video and 48kHz AAC audio. If the audio segment duration is not identical to
the corresponding video segment, the audio will be resegmented as to follow the
video segments as far as possible. Every audio segment will start less than one audio
frame after the video segment starts.

There are multiple ways to get content to the livesim2 server.

1. Use the bundled test content (only 8s or 12s long)
2. Fetch content that was used with [livesim1][1] from
   github at [livesim-content][livesim-content]
3. Use the `dashfetcher` tool to download a DASH asset
4. Use an existing VoD asset in `isoff-live` profile

livesim2 will scan all the segments of all representations and store metadata about
each segments timing in memory.

To avoid doing this at every startup, livesim2 supports storing and reading
such representation data from a file. The generation of such data is controlled via the `writerepdata` and `repdataroot` configuration parameters.

If such files are detected at startup, they will be used instead of scanning the files,
unles the `writerepdata` parameter is set.

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
http://localhost:8888/livesim2/testpic_2s/Manifest_thumbs.mpd
http://localhost:8888/livesim2/testpic_8s/Manifest.mpd
```

The default pattern provides MPDs with SegmentTemplate using `$Number$`. To stream with
SegmentTimeline with `$Time$`, one should add the parameter `/segtimeline_1` between
`livesim2` and the start of the asset path. For SegmentTimeline with `$Number$`, use
`/segtimelinenr_1` instead. Other parameters are added in a similar way.

Adding longer assets somewhere under the `vodroot` results in longer loops.
All sources are NTP synchronized (using the host machine clock) with a initial start
time given by availabilityStartTime and wrap every sequence duration after that.

### livesim-content at Github

In the repo [livesim-content][livesim-content], the content that was used for the
[livesim1 online][1-online] service is being gathered to make it easy to reproduce
the same use cases.

All content and features are not yet (2023-08-08) in place, but should be so before end
of October 2023.

To download and use that content, run

```sh
git clone https://github.com/Dash-Industry-Forum/livesim-content.git
```

and then set `--vodroot` to the `livesim-content` top directory or include that in
a bigger file tree.

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

#### Running dashfetcher

`dashfetcher` was created to facilitate download of DASH assets with lots of small segment files. One particular such source was the CTA-WAVE test content. However, that content is now also available as zip-files, so it is more efficient to download an unzip these files instead of making individual downloads of the segments.

For example, the asset above is also
available at `https://dash.akamaized.net/WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17/t3.zip`.

The `dashfetcher` binary can be found as `out/dashfetcher` after `make build`.

```sh
> dashfetcher --help
```

will provide a long help text that explains how to use it and will also provide an example URL to CTA-WAVE content.

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
More information can be found in the [deployment/README.md](deployment/README.md) file.

To get information about the available assets and other information
access the server's root URL.

### Docker

A `Dockerfile` is also provided. It builds a stand-alone livesim2 service.
A new image is automatically built and uploaded to the Github Container Registry (ghcr.io)
for each new release. To use the v1.7.0 image with a folder `test_streams` with
VoD assets, you can use docker compose to mount `test_streams` in the
default folder path `/vod` by using a compose.yml file like this:

```yaml
services:
  livesim2:
    image: ghcr.io/dash-industry-forum/livesim2/livesim2:v1.7.0
    ports:
      - 8888:8888
    volumes:
      - ./test_streams:/vod/test_streams
    restart: always
```

## List of functionality and options

The most direct information about the URL parameters
and how to find them, is available via the `/urlgen/`
page that can be reached once the server is running.
The livesim2 online page is [/urlgen/][urlgen].

The URL parameters are also listed on this project's Wiki page
[URL-parameters][urlparams]. Some more information
about how they are working is also available on the Wiki.

## Project and plan for new features/enhancement

The sponsored transition from livesim to livesim2 is covered on a [wiki page][l2-status].
On the [livesim2 project page][l2-project] the status of issues and proposed new ideas are listed.
Draft ideas are changed into [livesim2 issues][l2-issues] if prioritized.

## Sponsoring

It is possible to sponsor the project for further development. See the
[SPONSORING.md](SPONSORING.md) file for more information.

## License

See [LICENSE.md](LICENSE.md).

[1]: https://github.com/Dash-Industry-Forum/dash-live-source-simulator
[1-online]: https://livesim.dashif.org
[urlparams]: https://github.com/Dash-Industry-Forum/livesim2/wiki/URL-Parameters
[livesim-content]: https://github.com/Dash-Industry-Forum/livesim-content
[l2-project]: https://github.com/orgs/Dash-Industry-Forum/projects/7
[l2-issues]: https://github.com/Dash-Industry-Forum/livesim2/issues
[l2-status]: https://github.com/Dash-Industry-Forum/livesim2/wiki/Sponsored-transition-from-livesim1-to-livesim2
[urlgen]: https://livesim2.dashif.org/urlgen/
