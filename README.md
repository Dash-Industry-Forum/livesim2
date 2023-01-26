# livesim2 - The second generation DASH Live Source Simulator

`livesim2` is a new and improved version of the DASH-IF live source simulator.
This time it is written in Go instead of Python and designed to handle
content in a more flexible and versatile way.

It is intended to very easy to install and deploy locally by being compiled
into a single binary that serves the content via an HTTP/2 interface.

There is also a tool called `dashfetcher` that can be used to download
DASH VoD assets that can serve as sources for the live outputs.

The sources are looped so that an infinite "live" stream is available.

Similarly to [livesim](https://github.com/dash-Industry-Forum/dash-live-source-simulator),
the output is highly configurable by adding parameters inside the URLs.
These parameters are included not only in the MPD requests, but in
all segment requests allowing the server to be stateless, and
give the possibility to generate streams with a huge number of
parameter variations.

## Components

There are two main components, the server `livesim2` and the DASH VoD fetcher
`dashfetcher`.

### dashfetcher tool

The tool dashfetcher fetches DASH VoD assets via HTTP starting with an MPD.
Currently it supports MPDs with SegmentTimeline with `$Time$` and
SegmentTemplatewith `$Number$`. The assets must have no explicit `<BaseURL>` elements to
work properly. With the `-a/---auto` option, the path to the asset is preserved
as much as possible and adapted to the local path.

Files already downloaded will not be downloaded again, unless `-f/--force` is
used. To download a CTA-WAVE asset to use as an example, a possible command is

     dashfetcher --auto https://dash.akamaized.net/WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17/stream.mpd

which will result in a locally stored DASH VoD asset in the directory

     ./vod/WAVE/vectors/cfhd_sets/12.5_25_0t3/2022-10-17/

with an MPD called `stream.mpd` and the segments stored in subdirectories named after their relative
URLs. The download URL is stored in a file `mpdlist.json` which is read by livesim2, to provide
information about the asset.
One can have multiple MPDs in the same directory, which share the same representations.
That is an easy way to have variants with different representation combinations.

### livesim2 server

The server is configured in either of three different ways:

1. Default values
2. In a config file
3. Via command-line parameters
4. With environment variables

Two major values to configure are:

* the HTTP port being used (default: 8888)
* the top directory `vodroot` for searching for VoD assets to be used

Once the server is started, it will scan the file tree starting from
`vodroot` and gather metadata about all DASH VoD assets it finds.
Currently, only VoD assets using SegmentTimeline with `$Time$` and
SegmentTemplate with `$Number$`  are supported.

Once the server has started, it is possible to find out information about the server and the assets using the HTTP endpoints

* /config
* /healthz
* /assets
* /metrics

It is also possible to explore the file tree and play Vod assets by starting at

* /vod/...

Finally, any VoD MPD like `/vod/cfhd/stream.mpd` is available as a live stream by replacing `/vod/` with `livesim2`as like `/livesim2/cfhd/stream.mpd`.

### MPD Restrictions

The following restrictions are apply to the VoD manifest to be used with livesim2

* live-profile (separate segments)
* one Period with all representations of "same" duration
* no BaseURL elements
* no Location elements
* initialization and media attributes in SegmentTemplate on AdaptationSet level

### Test parameters

The query string parameter `?nowMS=...` can be used in any request towards the `livesim2` server
to set the wall-clock time livesim2 uses as local time. The time is measured with respect to
the 1970 Epoch start, and makes it possible to test requests in a deterministic way.

## Get Started
Install Go 1.19 or later.

Optionally, create a local `vendor` directory to keep a local copy of
all dependencies.

Then run

```sh
$ go mod tidy
```

to fetch and install all dependencies.

To build `dashfetcher` and `livesim2` do

```sh
$ cd cmd/dashfetcher
$ go build .
$ cd ../../cmd/livesim2
$ go build .
````

As usual for Go programs, one can also compile and run them directly with `go run .`.

### Testing the livesim2 server

A few very short test assets are bundled with the code.
That makes it possible to test the server by running

```sh
$ cd cmd/livesim2
$ ./livesim2 --vodroot=app/testdata/assets
```

The log will list the available assets and the port where the server runs.

and then stream the assets from URLs like:

`http://localhost:8888/livesim2/WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17/stream.mpd``

### Testing dashfetcher

```sh
$ cd cmd/livesim2
$ ./dashfetcher
```

will provide a help text that explains how to use it and will also provide an example URL
to CTA-WAVE content.

## License

See [LICENSE.md](LICENSE.md).
