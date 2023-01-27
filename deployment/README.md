# Deployment

Both `dashfetcher` and `livesim2` can be compiled to single binaries
on any target platform for the Go compiler such as Windows, MacOS, and Linux.
Since the result is a single binary it is easy to start anywhere.

On Linux, `livesim2` can run as a systemd service and do structured logging
using the journald API.

An example service file is provided here as `livesim2.service`.
Make sure that the binary and vod assets are available at the paths used in the service script.

The binary can also be started and log in more console-friendly formats.
See the help text provided with `livesim2 -h` to see the options.

## Cross compilation

Cross-compilation can be done like (Linux on Mac example)

    $ GOOS=linux GOARCH=amd64 go build .
